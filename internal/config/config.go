// Package config manages application configuration from various sources.
package config

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opencode-ai/opencode/internal/llm/models"
	"github.com/opencode-ai/opencode/internal/logging"
	"github.com/spf13/viper"
)

// MCPType defines the type of MCP (Model Control Protocol) server.
type MCPType string

// Supported MCP types
const (
	MCPStdio MCPType = "stdio"
	MCPSse   MCPType = "sse"
)

// MCPServer defines the configuration for a Model Control Protocol server.
type MCPServer struct {
	Command string            `json:"command"`
	Env     []string          `json:"env"`
	Args    []string          `json:"args"`
	Type    MCPType           `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type AgentName string

const (
	AgentCoder AgentName = "coder"
	AgentTask  AgentName = "task"
	AgentTitle AgentName = "title"
)

// Agent defines configuration for different LLM models and their token limits.
type Agent struct {
	Model           models.ModelID `json:"model"`
	MaxTokens       int64          `json:"maxTokens"`
	ReasoningEffort string         `json:"reasoningEffort"` // For openai models low,medium,heigh
}

// Provider defines configuration for an LLM provider.
type Provider struct {
	APIKey   string `json:"apiKey"`
	Disabled bool   `json:"disabled"`
}

// Data defines storage configuration.
type Data struct {
	Directory string `json:"directory"`
}

// LSPConfig defines configuration for Language Server Protocol integration.
type LSPConfig struct {
	Disabled bool     `json:"enabled"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	Options  any      `json:"options"`
}

// Config is the main configuration structure for the application.
type Config struct {
	Data       Data                              `json:"data"`
	WorkingDir string                            `json:"wd,omitempty"`
	MCPServers map[string]MCPServer              `json:"mcpServers,omitempty"`
	Providers  map[models.ModelProvider]Provider `json:"providers,omitempty"`
	LSP        map[string]LSPConfig              `json:"lsp,omitempty"`
	Agents     map[AgentName]Agent               `json:"agents"`
	Debug      bool                              `json:"debug,omitempty"`
	DebugLSP   bool                              `json:"debugLSP,omitempty"`
}

// Application constants
const (
	defaultDataDirectory = ".opencode"
	defaultLogLevel      = "info"
	appName              = "opencode"
)

// Global configuration instance
var cfg *Config

// Load initializes the configuration from environment variables and config files.
// If debug is true, debug mode is enabled and log level is set to debug.
// It returns an error if configuration loading fails.
func Load(workingDir string, debug bool) (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}

	cfg = &Config{
		WorkingDir: workingDir,
		MCPServers: make(map[string]MCPServer),
		Providers:  make(map[models.ModelProvider]Provider),
		LSP:        make(map[string]LSPConfig),
	}

	configureViper()
	setDefaults(debug)
	setProviderDefaults()

	// Read global config
	if err := readConfig(viper.ReadInConfig()); err != nil {
		return cfg, err
	}

	// Load and merge local config
	mergeLocalConfig(workingDir)

	// Apply configuration to the struct
	if err := viper.Unmarshal(cfg); err != nil {
		return cfg, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyDefaultValues()
	defaultLevel := slog.LevelInfo
	if cfg.Debug {
		defaultLevel = slog.LevelDebug
	}
	if os.Getenv("OPENCODE_DEV_DEBUG") == "true" {
		loggingFile := fmt.Sprintf("%s/%s", cfg.Data.Directory, "debug.log")

		// if file does not exist create it
		if _, err := os.Stat(loggingFile); os.IsNotExist(err) {
			if err := os.MkdirAll(cfg.Data.Directory, 0o755); err != nil {
				return cfg, fmt.Errorf("failed to create directory: %w", err)
			}
			if _, err := os.Create(loggingFile); err != nil {
				return cfg, fmt.Errorf("failed to create log file: %w", err)
			}
		}

		sloggingFileWriter, err := os.OpenFile(loggingFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			return cfg, fmt.Errorf("failed to open log file: %w", err)
		}
		// Configure logger
		logger := slog.New(slog.NewTextHandler(sloggingFileWriter, &slog.HandlerOptions{
			Level: defaultLevel,
		}))
		slog.SetDefault(logger)
	} else {
		// Configure logger
		logger := slog.New(slog.NewTextHandler(logging.NewWriter(), &slog.HandlerOptions{
			Level: defaultLevel,
		}))
		slog.SetDefault(logger)
	}

	// Validate configuration
	if err := Validate(); err != nil {
		return cfg, fmt.Errorf("config validation failed: %w", err)
	}

	if cfg.Agents == nil {
		cfg.Agents = make(map[AgentName]Agent)
	}

	// Override the max tokens for title agent
	cfg.Agents[AgentTitle] = Agent{
		Model:     cfg.Agents[AgentTitle].Model,
		MaxTokens: 80,
	}
	return cfg, nil
}

// configureViper sets up viper's configuration paths and environment variables.
func configureViper() {
	viper.SetConfigName(fmt.Sprintf(".%s", appName))
	viper.SetConfigType("json")
	viper.AddConfigPath("$HOME")
	viper.AddConfigPath(fmt.Sprintf("$XDG_CONFIG_HOME/%s", appName))
	viper.AddConfigPath(fmt.Sprintf("$HOME/.config/%s", appName))
	viper.SetEnvPrefix(strings.ToUpper(appName))
	viper.AutomaticEnv()
}

// setDefaults configures default values for configuration options.
func setDefaults(debug bool) {
	viper.SetDefault("data.directory", defaultDataDirectory)

	if debug {
		viper.SetDefault("debug", true)
		viper.Set("log.level", "debug")
	} else {
		viper.SetDefault("debug", false)
		viper.SetDefault("log.level", defaultLogLevel)
	}
}

// setProviderDefaults configures LLM provider defaults based on environment variables.
// the default model priority is:
// 1. Anthropic
// 2. OpenAI
// 3. Google Gemini
// 4. Groq
// 5. AWS Bedrock
// 6. Ollama
func setProviderDefaults() {
	// Anthropic configuration
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.anthropic.apiKey", apiKey)
		viper.SetDefault("agents.coder.model", models.Claude37Sonnet)
		viper.SetDefault("agents.task.model", models.Claude37Sonnet)
		viper.SetDefault("agents.title.model", models.Claude37Sonnet)
		return
	}

	// OpenAI configuration
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.openai.apiKey", apiKey)
		viper.SetDefault("agents.coder.model", models.GPT41)
		viper.SetDefault("agents.task.model", models.GPT41Mini)
		viper.SetDefault("agents.title.model", models.GPT41Mini)
		return
	}

	// Google Gemini configuration
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.gemini.apiKey", apiKey)
		viper.SetDefault("agents.coder.model", models.Gemini25)
		viper.SetDefault("agents.task.model", models.Gemini25Flash)
		viper.SetDefault("agents.title.model", models.Gemini25Flash)
		return
	}

	// Groq configuration
	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.groq.apiKey", apiKey)
		viper.SetDefault("agents.coder.model", models.QWENQwq)
		viper.SetDefault("agents.task.model", models.QWENQwq)
		viper.SetDefault("agents.title.model", models.QWENQwq)
		return
	}

	// AWS Bedrock configuration
	if hasAWSCredentials() {
		viper.SetDefault("agents.coder.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.task.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.title.model", models.BedrockClaude37Sonnet)
		return
	}
	
	// Ollama configuration (check if Ollama is running)
	if isOllamaRunning() {
		viper.SetDefault("providers.ollama.apiKey", "local") // Not used but needed for config validation
		viper.SetDefault("agents.coder.model", models.OllamaCodeLlama)
		viper.SetDefault("agents.task.model", models.OllamaLlama3)
		viper.SetDefault("agents.title.model", models.OllamaLlama3)
		return
	}
}

// hasAWSCredentials checks if AWS credentials are available in the environment.
func hasAWSCredentials() bool {
	// Check for explicit AWS credentials
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return true
	}

	// Check for AWS profile
	if os.Getenv("AWS_PROFILE") != "" || os.Getenv("AWS_DEFAULT_PROFILE") != "" {
		return true
	}

	// Check for AWS region
	if os.Getenv("AWS_REGION") != "" || os.Getenv("AWS_DEFAULT_REGION") != "" {
		return true
	}

	// Check if running on EC2 with instance profile
	if os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" {
		return true
	}

	return false
}

// isOllamaRunning checks if Ollama is running by making a request to its API.
func isOllamaRunning() bool {
	client := &http.Client{
		Timeout: 2 * time.Second, // Short timeout for quick check
	}
	
	// Try to connect to Ollama API
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == http.StatusOK
}

// readConfig handles the result of reading a configuration file.
func readConfig(err error) error {
	if err == nil {
		return nil
	}

	// It's okay if the config file doesn't exist
	if _, ok := err.(viper.ConfigFileNotFoundError); ok {
		return nil
	}

	return fmt.Errorf("failed to read config: %w", err)
}

// mergeLocalConfig loads and merges configuration from the local directory.
func mergeLocalConfig(workingDir string) {
	local := viper.New()
	local.SetConfigName(fmt.Sprintf(".%s", appName))
	local.SetConfigType("json")
	local.AddConfigPath(workingDir)

	// Merge local config if it exists
	if err := local.ReadInConfig(); err == nil {
		viper.MergeConfigMap(local.AllSettings())
	}
}

// applyDefaultValues sets default values for configuration fields that need processing.
func applyDefaultValues() {
	// Set default MCP type if not specified
	for k, v := range cfg.MCPServers {
		if v.Type == "" {
			v.Type = MCPStdio
			cfg.MCPServers[k] = v
		}
	}
}

// Validate checks if the configuration is valid and applies defaults where needed.
// It validates model IDs and providers, ensuring they are supported.
func Validate() error {
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	// Validate agent models
	for name, agent := range cfg.Agents {
		// Check if model exists
		model, modelExists := models.SupportedModels[agent.Model]
		if !modelExists {
			logging.Warn("unsupported model configured, reverting to default",
				"agent", name,
				"configured_model", agent.Model)

			// Set default model based on available providers
			if setDefaultModelForAgent(name) {
				logging.Info("set default model for agent", "agent", name, "model", cfg.Agents[name].Model)
			} else {
				return fmt.Errorf("no valid provider available for agent %s", name)
			}
			continue
		}

		// Check if provider for the model is configured
		provider := model.Provider
		providerCfg, providerExists := cfg.Providers[provider]

		if !providerExists {
			// Provider not configured, check if we have environment variables
			apiKey := getProviderAPIKey(provider)
			if apiKey == "" {
				logging.Warn("provider not configured for model, reverting to default",
					"agent", name,
					"model", agent.Model,
					"provider", provider)

				// Set default model based on available providers
				if setDefaultModelForAgent(name) {
					logging.Info("set default model for agent", "agent", name, "model", cfg.Agents[name].Model)
				} else {
					return fmt.Errorf("no valid provider available for agent %s", name)
				}
			} else {
				// Add provider with API key from environment
				cfg.Providers[provider] = Provider{
					APIKey: apiKey,
				}
				logging.Info("added provider from environment", "provider", provider)
			}
		} else if providerCfg.Disabled || providerCfg.APIKey == "" {
			// Provider is disabled or has no API key
			logging.Warn("provider is disabled or has no API key, reverting to default",
				"agent", name,
				"model", agent.Model,
				"provider", provider)

			// Set default model based on available providers
			if setDefaultModelForAgent(name) {
				logging.Info("set default model for agent", "agent", name, "model", cfg.Agents[name].Model)
			} else {
				return fmt.Errorf("no valid provider available for agent %s", name)
			}
		}

		// Validate max tokens
		if agent.MaxTokens <= 0 {
			logging.Warn("invalid max tokens, setting to default",
				"agent", name,
				"model", agent.Model,
				"max_tokens", agent.MaxTokens)

			// Update the agent with default max tokens
			updatedAgent := cfg.Agents[name]
			if model.DefaultMaxTokens > 0 {
				updatedAgent.MaxTokens = model.DefaultMaxTokens
			} else {
				updatedAgent.MaxTokens = 4096 // Fallback default
			}
			cfg.Agents[name] = updatedAgent
		} else if model.ContextWindow > 0 && agent.MaxTokens > model.ContextWindow/2 {
			// Ensure max tokens doesn't exceed half the context window (reasonable limit)
			logging.Warn("max tokens exceeds half the context window, adjusting",
				"agent", name,
				"model", agent.Model,
				"max_tokens", agent.MaxTokens,
				"context_window", model.ContextWindow)

			// Update the agent with adjusted max tokens
			updatedAgent := cfg.Agents[name]
			updatedAgent.MaxTokens = model.ContextWindow / 2
			cfg.Agents[name] = updatedAgent
		}

		// Validate reasoning effort for models that support reasoning
		if model.CanReason && provider == models.ProviderOpenAI {
			if agent.ReasoningEffort == "" {
				// Set default reasoning effort for models that support it
				logging.Info("setting default reasoning effort for model that supports reasoning",
					"agent", name,
					"model", agent.Model)

				// Update the agent with default reasoning effort
				updatedAgent := cfg.Agents[name]
				updatedAgent.ReasoningEffort = "medium"
				cfg.Agents[name] = updatedAgent
			} else {
				// Check if reasoning effort is valid (low, medium, high)
				effort := strings.ToLower(agent.ReasoningEffort)
				if effort != "low" && effort != "medium" && effort != "high" {
					logging.Warn("invalid reasoning effort, setting to medium",
						"agent", name,
						"model", agent.Model,
						"reasoning_effort", agent.ReasoningEffort)

					// Update the agent with valid reasoning effort
					updatedAgent := cfg.Agents[name]
					updatedAgent.ReasoningEffort = "medium"
					cfg.Agents[name] = updatedAgent
				}
			}
		} else if !model.CanReason && agent.ReasoningEffort != "" {
			// Model doesn't support reasoning but reasoning effort is set
			logging.Warn("model doesn't support reasoning but reasoning effort is set, ignoring",
				"agent", name,
				"model", agent.Model,
				"reasoning_effort", agent.ReasoningEffort)

			// Update the agent to remove reasoning effort
			updatedAgent := cfg.Agents[name]
			updatedAgent.ReasoningEffort = ""
			cfg.Agents[name] = updatedAgent
		}
	}

	// Validate providers
	for provider, providerCfg := range cfg.Providers {
		if providerCfg.APIKey == "" && !providerCfg.Disabled {
			logging.Warn("provider has no API key, marking as disabled", "provider", provider)
			providerCfg.Disabled = true
			cfg.Providers[provider] = providerCfg
		}
	}

	// Validate LSP configurations
	for language, lspConfig := range cfg.LSP {
		if lspConfig.Command == "" && !lspConfig.Disabled {
			logging.Warn("LSP configuration has no command, marking as disabled", "language", language)
			lspConfig.Disabled = true
			cfg.LSP[language] = lspConfig
		}
	}

	return nil
}

// getProviderAPIKey gets the API key for a provider from environment variables
func getProviderAPIKey(provider models.ModelProvider) string {
	switch provider {
	case models.ProviderAnthropic:
		return os.Getenv("ANTHROPIC_API_KEY")
	case models.ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	case models.ProviderGemini:
		return os.Getenv("GEMINI_API_KEY")
	case models.ProviderGROQ:
		return os.Getenv("GROQ_API_KEY")
	case models.ProviderBedrock:
		if hasAWSCredentials() {
			return "aws-credentials-available"
		}
	case models.ProviderOllama:
		if isOllamaRunning() {
			return "local" // Not actually used for authentication
		}
	}
	return ""
}

// setDefaultModelForAgent sets a default model for an agent based on available providers
func setDefaultModelForAgent(agent AgentName) bool {
	// Check providers in order of preference
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}
		cfg.Agents[agent] = Agent{
			Model:     models.Claude37Sonnet,
			MaxTokens: maxTokens,
		}
		return true
	}

	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		var model models.ModelID
		maxTokens := int64(5000)
		reasoningEffort := ""

		switch agent {
		case AgentTitle:
			model = models.GPT41Mini
			maxTokens = 80
		case AgentTask:
			model = models.GPT41Mini
		default:
			model = models.GPT41
		}

		// Check if model supports reasoning
		if modelInfo, ok := models.SupportedModels[model]; ok && modelInfo.CanReason {
			reasoningEffort = "medium"
		}

		cfg.Agents[agent] = Agent{
			Model:           model,
			MaxTokens:       maxTokens,
			ReasoningEffort: reasoningEffort,
		}
		return true
	}

	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		var model models.ModelID
		maxTokens := int64(5000)

		if agent == AgentTitle {
			model = models.Gemini25Flash
			maxTokens = 80
		} else {
			model = models.Gemini25
		}

		cfg.Agents[agent] = Agent{
			Model:     model,
			MaxTokens: maxTokens,
		}
		return true
	}

	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}

		cfg.Agents[agent] = Agent{
			Model:     models.QWENQwq,
			MaxTokens: maxTokens,
		}
		return true
	}

	if hasAWSCredentials() {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}

		cfg.Agents[agent] = Agent{
			Model:           models.BedrockClaude37Sonnet,
			MaxTokens:       maxTokens,
			ReasoningEffort: "medium", // Claude models support reasoning
		}
		return true
	}
	
	if isOllamaRunning() {
		var model models.ModelID
		maxTokens := int64(4096)
		
		switch agent {
		case AgentTitle:
			model = models.OllamaLlama3
			maxTokens = 80
		case AgentTask:
			model = models.OllamaLlama3
		default:
			model = models.OllamaCodeLlama
		}
		
		cfg.Agents[agent] = Agent{
			Model:     model,
			MaxTokens: maxTokens,
		}
		return true
	}

	return false
}

// Get returns the current configuration.
// It's safe to call this function multiple times.
func Get() *Config {
	return cfg
}

// WorkingDirectory returns the current working directory from the configuration.
func WorkingDirectory() string {
	if cfg == nil {
		panic("config not loaded")
	}
	return cfg.WorkingDir
}

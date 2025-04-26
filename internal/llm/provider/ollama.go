package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/opencode-ai/opencode/internal/config"
	"github.com/opencode-ai/opencode/internal/llm/tools"
	"github.com/opencode-ai/opencode/internal/logging"
	"github.com/opencode-ai/opencode/internal/message"
)

type ollamaOptions struct {
	baseURL string
	model   string
}

type OllamaOption func(*ollamaOptions)

type ollamaClient struct {
	providerOptions providerClientOptions
	options         ollamaOptions
	client          *http.Client
}

type OllamaClient interface {
	ProviderClient
}

// Ollama API request/response structures
type ollamaRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaResponse struct {
	Model     string        `json:"model"`
	CreatedAt string        `json:"created_at"`
	Message   ollamaMessage `json:"message"`
	Done      bool          `json:"done"`
	Error     string        `json:"error,omitempty"`
	Usage     ollamaUsage   `json:"usage,omitempty"`
}

type ollamaUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func newOllamaClient(opts providerClientOptions) OllamaClient {
	ollamaOpts := ollamaOptions{
		baseURL: "http://localhost:11434",
	}
	
	// If the model is OllamaCustom, use the model name from options
	if opts.model.ID == "ollama.custom" && opts.model.APIModel != "" {
		ollamaOpts.model = opts.model.APIModel
	} else {
		ollamaOpts.model = opts.model.APIModel
	}

	for _, o := range opts.ollamaOptions {
		o(&ollamaOpts)
	}

	client := &http.Client{
		Timeout: time.Second * 300, // 5 minute timeout
	}

	return &ollamaClient{
		providerOptions: opts,
		options:         ollamaOpts,
		client:          client,
	}
}

func (o *ollamaClient) convertMessages(messages []message.Message) []ollamaMessage {
	ollamaMessages := []ollamaMessage{}

	// Add system message first if present
	if o.providerOptions.systemMessage != "" {
		ollamaMessages = append(ollamaMessages, ollamaMessage{
			Role:    "system",
			Content: o.providerOptions.systemMessage,
		})
	}

	for _, msg := range messages {
		switch msg.Role {
		case message.User:
			ollamaMessages = append(ollamaMessages, ollamaMessage{
				Role:    "user",
				Content: msg.Content().String(),
			})

		case message.Assistant:
			if msg.Content().String() != "" {
				ollamaMessages = append(ollamaMessages, ollamaMessage{
					Role:    "assistant",
					Content: msg.Content().String(),
				})
			}

			// Ollama doesn't support tool calls directly, so we'll convert them to text
			if len(msg.ToolCalls()) > 0 {
				toolCallsText := "I need to use the following tools:\n"
				for _, call := range msg.ToolCalls() {
					toolCallsText += fmt.Sprintf("- Tool: %s\n  Arguments: %s\n", call.Name, call.Input)
				}
				ollamaMessages = append(ollamaMessages, ollamaMessage{
					Role:    "assistant",
					Content: toolCallsText,
				})
			}

		case message.Tool:
			// Convert tool results to user messages as Ollama doesn't have a tool role
			for _, result := range msg.ToolResults() {
				ollamaMessages = append(ollamaMessages, ollamaMessage{
					Role:    "user",
					Content: fmt.Sprintf("Tool result for %s: %s", result.ToolCallID, result.Content),
				})
			}
		}
	}

	return ollamaMessages
}

func (o *ollamaClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	ollamaMessages := o.convertMessages(messages)
	
	// Prepare the request
	reqBody := ollamaRequest{
		Model:    o.options.model,
		Messages: ollamaMessages,
		Stream:   false,
	}
	
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	
	cfg := config.Get()
	if cfg.Debug {
		logging.Debug("Ollama request", "request", string(jsonData))
	}
	
	// Create the HTTP request
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		fmt.Sprintf("%s/api/chat", o.options.baseURL),
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	// Send the request
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	
	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama API error: %s", string(body))
	}
	
	// Parse the response
	var ollamaResp ollamaResponse
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	
	if ollamaResp.Error != "" {
		return nil, fmt.Errorf("ollama API error: %s", ollamaResp.Error)
	}
	
	// Create the provider response
	return &ProviderResponse{
		Content: ollamaResp.Message.Content,
		Usage: TokenUsage{
			InputTokens:  ollamaResp.Usage.PromptTokens,
			OutputTokens: ollamaResp.Usage.CompletionTokens,
		},
		FinishReason: message.FinishReasonEndTurn,
	}, nil
}

func (o *ollamaClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	ollamaMessages := o.convertMessages(messages)
	
	// Prepare the request
	reqBody := ollamaRequest{
		Model:    o.options.model,
		Messages: ollamaMessages,
		Stream:   true,
	}
	
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		eventChan := make(chan ProviderEvent, 1)
		eventChan <- ProviderEvent{
			Type:  EventError,
			Error: fmt.Errorf("failed to marshal request: %w", err),
		}
		close(eventChan)
		return eventChan
	}
	
	cfg := config.Get()
	if cfg.Debug {
		logging.Debug("Ollama stream request", "request", string(jsonData))
	}
	
	// Create the HTTP request
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		fmt.Sprintf("%s/api/chat", o.options.baseURL),
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		eventChan := make(chan ProviderEvent, 1)
		eventChan <- ProviderEvent{
			Type:  EventError,
			Error: fmt.Errorf("failed to create request: %w", err),
		}
		close(eventChan)
		return eventChan
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	eventChan := make(chan ProviderEvent)
	
	go func() {
		defer close(eventChan)
		
		// Send the request
		resp, err := o.client.Do(req)
		if err != nil {
			eventChan <- ProviderEvent{
				Type:  EventError,
				Error: fmt.Errorf("failed to send request: %w", err),
			}
			return
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			eventChan <- ProviderEvent{
				Type:  EventError,
				Error: fmt.Errorf("ollama API error: %s", string(body)),
			}
			return
		}
		
		// Process the streaming response
		reader := bufio.NewReader(resp.Body)
		fullContent := ""
		
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				eventChan <- ProviderEvent{
					Type:  EventError,
					Error: fmt.Errorf("error reading stream: %w", err),
				}
				return
			}
			
			if len(line) == 0 {
				continue
			}
			
			// Parse the JSON line
			var ollamaResp ollamaResponse
			if err := json.Unmarshal(line, &ollamaResp); err != nil {
				eventChan <- ProviderEvent{
					Type:  EventError,
					Error: fmt.Errorf("failed to unmarshal response: %w", err),
				}
				return
			}
			
			if ollamaResp.Error != "" {
				eventChan <- ProviderEvent{
					Type:  EventError,
					Error: fmt.Errorf("ollama API error: %s", ollamaResp.Error),
				}
				return
			}
			
			// Send content delta event
			if ollamaResp.Message.Content != "" {
				eventChan <- ProviderEvent{
					Type:    EventContentDelta,
					Content: ollamaResp.Message.Content,
				}
				fullContent += ollamaResp.Message.Content
			}
			
			// If done, send complete event
			if ollamaResp.Done {
				eventChan <- ProviderEvent{
					Type: EventComplete,
					Response: &ProviderResponse{
						Content: fullContent,
						Usage: TokenUsage{
							InputTokens:  ollamaResp.Usage.PromptTokens,
							OutputTokens: ollamaResp.Usage.CompletionTokens,
						},
						FinishReason: message.FinishReasonEndTurn,
					},
				}
				return
			}
		}
		
		// If we get here without a done event, send a complete event anyway
		eventChan <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content:      fullContent,
				FinishReason: message.FinishReasonEndTurn,
			},
		}
	}()
	
	return eventChan
}

func WithOllamaBaseURL(baseURL string) OllamaOption {
	return func(options *ollamaOptions) {
		options.baseURL = baseURL
	}
}

func WithOllamaModel(model string) OllamaOption {
	return func(options *ollamaOptions) {
		options.model = model
	}
}
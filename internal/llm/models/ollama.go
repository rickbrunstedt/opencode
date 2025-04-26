package models

const (
	// Ollama models
	OllamaLlama3   ModelID = "ollama.llama3"
	OllamaCodeLlama ModelID = "ollama.codellama"
	OllamaMistral  ModelID = "ollama.mistral"
	OllamaCustom   ModelID = "ollama.custom"
)

const (
	ProviderOllama ModelProvider = "ollama"
)

var OllamaModels = map[ModelID]Model{
	OllamaLlama3: {
		ID:               OllamaLlama3,
		Name:             "Ollama: Llama 3",
		Provider:         ProviderOllama,
		APIModel:         "llama3",
		CostPer1MIn:      0,
		CostPer1MOut:     0,
		CostPer1MInCached:  0,
		CostPer1MOutCached: 0,
		ContextWindow:    8192,
		DefaultMaxTokens: 4096,
		CanReason:        false,
	},
	OllamaCodeLlama: {
		ID:               OllamaCodeLlama,
		Name:             "Ollama: CodeLlama",
		Provider:         ProviderOllama,
		APIModel:         "codellama",
		CostPer1MIn:      0,
		CostPer1MOut:     0,
		CostPer1MInCached:  0,
		CostPer1MOutCached: 0,
		ContextWindow:    8192,
		DefaultMaxTokens: 4096,
		CanReason:        false,
	},
	OllamaMistral: {
		ID:               OllamaMistral,
		Name:             "Ollama: Mistral",
		Provider:         ProviderOllama,
		APIModel:         "mistral",
		CostPer1MIn:      0,
		CostPer1MOut:     0,
		CostPer1MInCached:  0,
		CostPer1MOutCached: 0,
		ContextWindow:    8192,
		DefaultMaxTokens: 4096,
		CanReason:        false,
	},
	OllamaCustom: {
		ID:               OllamaCustom,
		Name:             "Ollama: Custom Model",
		Provider:         ProviderOllama,
		APIModel:         "",  // Will be set from config
		CostPer1MIn:      0,
		CostPer1MOut:     0,
		CostPer1MInCached:  0,
		CostPer1MOutCached: 0,
		ContextWindow:    8192,
		DefaultMaxTokens: 4096,
		CanReason:        false,
	},
}
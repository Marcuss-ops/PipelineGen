package types

import "encoding/json"

// Message rappresenta un messaggio chat
type Message struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // Base64 encoded images
}

// ChatRequest richiesta chat
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	// Think disables Gemma's reasoning channel for production text
	// generation. Without this explicit false, Gemma can consume the
	// entire num_predict budget in message.thinking and return an empty
	// message.content.
	Think   *bool          `json:"think,omitempty"`
	Options map[string]any `json:"options,omitempty"`
	// Format forces Ollama's native JSON-mode at the wire-format
	// layer (top-level body field, NOT inside Options). P0.2
	// (June 2026): pass through from TextGenerationRequest.Format
	// so the script-generation adapter can drive Ollama into
	// JSON-mode when OutputModeScriptV1 is requested.
	Format json.RawMessage `json:"format,omitempty"`
}

// ChatResponse risposta chat
type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

// GenerateRequest richiesta generazione (Legacy API)
type GenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Context []int          `json:"context,omitempty"`
	Stream  bool           `json:"stream"`
	Images  []string       `json:"images,omitempty"` // Base64 encoded images
	Format  any            `json:"format,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// GenerateResponse risposta generazione (Legacy API)
type GenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Context  []int  `json:"context,omitempty"`
}

// EmbedRequest richiesta embedding
type EmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// EmbedResponse risposta embedding
type EmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Model rappresenta un modello Ollama
type Model struct {
	Name string `json:"name"`
}

// ListModelsResponse risposta lista modelli
type ListModelsResponse struct {
	Models []Model `json:"models"`
}

package ports

import "context"

// TextRequest is the generic input for an LLM text-generation call.
// It is intentionally framework-agnostic: the legacy Ollama types
// (ollamatypes.TextGenerationRequest) WILL be adapted to TextRequest in
// the Ollama-based adapter that Agent 1 will provide.
type TextRequest struct {
	Prompt       string   `json:"prompt"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	Model        string   `json:"model,omitempty"`
	Temperature  float64  `json:"temperature,omitempty"`
	MaxTokens    int      `json:"max_tokens,omitempty"`
	Stop         []string `json:"stop,omitempty"`
	JSONMode     bool     `json:"json_mode,omitempty"`
}

// TextResponse is the generic output of an LLM text call. Token counts
// are optional but recommended so use cases can report progress.
type TextResponse struct {
	Text         string `json:"text"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	DurationMs   int64  `json:"duration_ms"`
}

// LLMGenerator is the local contract for "produce text from a prompt".
// Any adapter (Ollama, OpenAI, vLLM, ...) may satisfy it.
//
// JSONMode helpers are required so the planning / metadata phases can
// call the model with a typed output schema.
type LLMGenerator interface {
	Generate(ctx context.Context, req TextRequest) (*TextResponse, error)
	GenerateJSON(ctx context.Context, req TextRequest, out any) error
}

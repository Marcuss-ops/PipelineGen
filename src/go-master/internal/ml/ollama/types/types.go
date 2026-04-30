// Package ollama provides types for Ollama API integration.
package types

// TextGenerationRequest request per generazione script da testo
type TextGenerationRequest struct {
	SourceText string                 `json:"source_text" binding:"required"`
	Title      string                 `json:"title" binding:"required"`
	Language   string                 `json:"language"`
	Duration   int                    `json:"duration"` // secondi
	Tone       string                 `json:"tone"`
	Model      string                 `json:"model"`
	Options    map[string]interface{} `json:"options,omitempty"`
}

// YouTubeGenerationRequest request per generazione script da YouTube
type YouTubeGenerationRequest struct {
	YouTubeURL string                 `json:"youtube_url" binding:"required"`
	Title      string                 `json:"title" binding:"required"`
	Language   string                 `json:"language"`
	Duration   int                    `json:"duration"`
	Model      string                 `json:"model"`
	Options    map[string]interface{} `json:"options,omitempty"`
}

// RegenerationRequest request per rigenerazione script
type RegenerationRequest struct {
	OriginalScript string                 `json:"original_script" binding:"required"`
	Title          string                 `json:"title"`
	Language       string                 `json:"language"`
	Tone           string                 `json:"tone"`
	Model          string                 `json:"model"`
	Options        map[string]interface{} `json:"options,omitempty"`
}

// GenerationResult risultato generazione script
type GenerationResult struct {
	Script      string `json:"script"`
	WordCount   int    `json:"word_count"`
	EstDuration int    `json:"est_duration"` // stima in secondi
	Model       string `json:"model"`
	Prompt      string `json:"prompt,omitempty"` // per debugging
}

// Model info su modello disponibile
type Model struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

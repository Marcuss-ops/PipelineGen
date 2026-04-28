// Package ollama provides types for Ollama API integration.
package types

// TextGenerationRequest request per generazione script da testo
type TextGenerationRequest struct {
	SourceText string                 `json:"source_text" binding:"required"`
	Title      string                 `json:"title" binding:"required"`
	Language   string                 `json:"language" default:"italian"`
	Duration   int                    `json:"duration" default:"60"` // secondi
	Tone       string                 `json:"tone" default:"professional"`
	Model      string                 `json:"model" default:"gemma3:4b"`
	Options    map[string]interface{} `json:"options,omitempty"`
}

// YouTubeGenerationRequest request per generazione script da YouTube
type YouTubeGenerationRequest struct {
	YouTubeURL string                 `json:"youtube_url" binding:"required"`
	Title      string                 `json:"title" binding:"required"`
	Language   string                 `json:"language" default:"italian"`
	Duration   int                    `json:"duration" default:"60"`
	Model      string                 `json:"model" default:"gemma3:4b"`
	Options    map[string]interface{} `json:"options,omitempty"`
}

// RegenerationRequest request per rigenerazione script
type RegenerationRequest struct {
	OriginalScript string                 `json:"original_script" binding:"required"`
	Title          string                 `json:"title"`
	Language       string                 `json:"language" default:"italian"`
	Tone           string                 `json:"tone" default:"professional"`
	Model          string                 `json:"model" default:"gemma3:4b"`
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

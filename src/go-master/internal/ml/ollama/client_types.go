package ollama

import "net/http"

// Client client per Ollama API
type Client struct {
	baseURL    string
	httpClient *http.Client
	model      string
}

// Message rappresenta un messaggio chat
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest richiesta chat
type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []Message              `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

// ChatResponse risposta chat
type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

// GenerateRequest richiesta generazione (Legacy API)
type GenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Context []int                  `json:"context,omitempty"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options,omitempty"`
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

// ListModelsResponse risposta lista modelli
type ListModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo info su un modello
type ModelInfo struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

// EntityExtractionRequest represents a request to extract entities from a segment
type EntityExtractionRequest struct {
	SegmentText  string `json:"segment_text"`
	SegmentIndex int    `json:"segment_index"`
	EntityCount  int    `json:"entity_count"`
}

// EntityExtractionResult represents the result of entity extraction for a segment
type EntityExtractionResult struct {
	SegmentIndex     int               `json:"segment_index"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
}

// SegmentEntities represents extracted entities for a single segment
type SegmentEntities struct {
	SegmentIndex     int               `json:"segment_index"`
	SegmentText      string            `json:"segment_text"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
}

// FullEntityAnalysis represents the complete entity analysis for a script
type FullEntityAnalysis struct {
	TotalSegments         int               `json:"total_segments"`
	SegmentEntities       []SegmentEntities `json:"segment_entities"`
	TotalEntities         int               `json:"total_entities"`
	EntityCountPerSegment int               `json:"entity_count_per_segment"`
}

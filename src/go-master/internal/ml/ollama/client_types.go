package ollama

import (
	"net/http"
	"sync"
	"time"
)

// CircuitBreaker implements a simple circuit breaker for Ollama requests
type CircuitBreaker struct {
	mu              sync.Mutex
	state           string // "closed", "open", "half-open"
	failureCount    int
	lastFailureTime time.Time
	maxFailures     int
	timeout         time.Duration
}

func NewCircuitBreaker(maxFailures int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       "closed",
		maxFailures: maxFailures,
		timeout:     timeout,
	}
}

func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case "closed":
		return true
	case "open":
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = "half-open"
			return true
		}
		return false
	case "half-open":
		return true
	}
	return false
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = "closed"
	cb.failureCount = 0
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	if cb.failureCount >= cb.maxFailures {
		cb.state = "open"
	}
}

// ModelFallback defines fallback model chains
var modelFallbackChains = map[string][]string{
	"qwen2.5:12b":  {"qwen2.5:7b", "gemma3:4b"},
	"llama3.2:12b": {"llama3.2:7b", "gemma3:4b"},
	"mistral:12b":  {"mistral:7b", "gemma3:4b"},
	"gemma3:12b":   {"gemma3:4b"},
	"qwen2.5:7b":   {"gemma3:4b"},
	"llama3.2:7b":  {"gemma3:4b"},
}

// Client client per Ollama API
type Client struct {
	baseURL        string
	httpClient     *http.Client
	model          string
	circuitBreaker *CircuitBreaker
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
	SegmentIndex     int                 `json:"segment_index"`
	FrasiImportanti  []string            `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string   `json:"entity_senza_testo"`
	NomiSpeciali     []string            `json:"nomi_speciali"`
	ParoleImportanti []string            `json:"parole_importanti"`
	ArtlistPhrases   map[string][]string `json:"artlist_phrases"`
}

// SegmentEntities represents extracted entities for a single segment
type SegmentEntities struct {
	SegmentIndex     int                 `json:"segment_index"`
	SegmentText      string              `json:"segment_text"`
	FrasiImportanti  []string            `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string   `json:"entity_senza_testo"`
	NomiSpeciali     []string            `json:"nomi_speciali"`
	ParoleImportanti []string            `json:"parole_importanti"`
	ArtlistPhrases   map[string][]string `json:"artlist_phrases"`
	ArtlistMatches   map[string][]string `json:"artlist_matches"`
}

// FullEntityAnalysis represents the complete entity analysis for a script
type FullEntityAnalysis struct {
	TotalSegments         int               `json:"total_segments"`
	SegmentEntities       []SegmentEntities `json:"segment_entities"`
	TotalEntities         int               `json:"total_entities"`
	EntityCountPerSegment int               `json:"entity_count_per_segment"`
}

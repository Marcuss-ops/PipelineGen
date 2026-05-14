package clipindexer

import "time"

// IndexResult represents the result of an indexing operation
type IndexResult struct {
	ClipID            string    `json:"clip_id"`
	Success           bool      `json:"success"`
	Error             string    `json:"error,omitempty"`
	IndexedAt         time.Time `json:"indexed_at"`
	SearchText        string    `json:"search_text,omitempty"`
	Category          string    `json:"category,omitempty"`
	EmbeddingComputed bool      `json:"embedding_computed"`
}

// BatchIndexRequest represents a batch indexing request
type BatchIndexRequest struct {
	ClipIDs []string `json:"clip_ids"`
}

// BatchIndexResponse represents a batch indexing response
type BatchIndexResponse struct {
	Total      int           `json:"total"`
	Successful int           `json:"successful"`
	Failed     int           `json:"failed"`
	Results    []IndexResult `json:"results"`
}

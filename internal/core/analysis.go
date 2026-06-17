// Package core provides canonical shared types for the PipelineGen system.
// Analysis types moved here from internal/ml/ollama/types to prevent
// cross-layer imports from handler packages into the ML layer.
package core

// EntityExtractionRequest represents a request to extract entities from a segment.
type EntityExtractionRequest struct {
	SegmentText  string `json:"segment_text"`
	SegmentIndex int    `json:"segment_index"`
	EntityCount  int    `json:"entity_count"`
}

// EntityExtractionResult represents the result of entity extraction for a segment.
type EntityExtractionResult struct {
	SegmentIndex     int               `json:"segment_index"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
	ArtlistPhrases   []string          `json:"artlist_phrases"`
}

// SegmentEntities represents extracted entities for a single segment.
type SegmentEntities struct {
	SegmentIndex     int                 `json:"segment_index"`
	SegmentText      string              `json:"segment_text"`
	FrasiImportanti  []string            `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string   `json:"entity_senza_testo"`
	NomiSpeciali     []string            `json:"nomi_speciali"`
	ParoleImportanti []string            `json:"parole_importanti"`
	ArtlistPhrases   []string            `json:"artlist_phrases"`
	ArtlistMatches   map[string][]string `json:"artlist_matches"`
}

// FullEntityAnalysis represents the complete entity analysis for a script.
type FullEntityAnalysis struct {
	TotalSegments         int               `json:"total_segments"`
	SegmentEntities       []SegmentEntities `json:"segment_entities"`
	TotalEntities         int               `json:"total_entities"`
	EntityCountPerSegment int               `json:"entity_count_per_segment"`
}

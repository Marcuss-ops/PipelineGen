// Package entities provides entity extraction and script analysis.
package entities

// Category represents an entity category type
type Category string

const (
	FrasiImportanti  Category = "Frasi_Importanti"
	EntitaSenzaTesto Category = "Entita_Senza_Testo"
	NomiSpeciali     Category = "Nomi_Speciali"
	ParoleImportanti Category = "Parole_Importanti"
)

// Entity represents a single extracted entity with metadata
type Entity struct {
	Text      string   `json:"text"`
	Category  Category `json:"category"`
	Confidence float64 `json:"confidence,omitempty"`
	ImageURL  string   `json:"image_url,omitempty"`  // Only for Entita_Senza_Testo
	Source    string   `json:"source,omitempty"`      // Extraction method/source
}

// SegmentEntityResult represents extracted entities for a single segment
type SegmentEntityResult struct {
	SegmentIndex     int               `json:"segment_index"`
	SegmentText      string            `json:"segment_text"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
}

// ScriptEntityAnalysis represents the complete entity analysis for a script
type ScriptEntityAnalysis struct {
	Script                string                 `json:"script"`
	TotalSegments         int                    `json:"total_segments"`
	SegmentEntities       []SegmentEntityResult  `json:"segment_entities"`
	TotalEntities         int                    `json:"total_entities"`
	EntityCountPerSegment int                    `json:"entity_count_per_segment"`
}

// SegmentConfig holds configuration for segmentation
type SegmentConfig struct {
	TargetWordsPerSegment int
	MinSegments           int
	MaxSegments           int
	OverlapWords          int
}

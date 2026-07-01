package asset

// GeneratedImageDetail is the per-asset provenance row for AI-generated
// images. FASE 4A EXPAND (July 2026, image-territories action plan): typed
// mirror of metadata_json keys that previously held the same data.
type GeneratedImageDetail struct {
	AssetID         string
	PromptOriginal  string
	PromptResolved  string
	StyleID         string
	StyleVersion    string
	Model           string
	Seed            int64
	GenerationJobID string
	SourceHash      string
}

// RetrievedImageDetail is the per-asset detail row for web-retrieved
// images. FASE 4A EXPAND (July 2026).
type RetrievedImageDetail struct {
	AssetID        string
	SourceImageURL string
	SourcePageURL  string
	License        string
	Author         string
	SearchQuery    string
	RetrievedAt    string
	Provider       string
}

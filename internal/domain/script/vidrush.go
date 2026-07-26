// Package script — vidrush.go owns the canonical per-segment VidRush
// result shapes used by script generation.
package script

// CanonicalSegment is the stable segment representation used for
// VidRush extraction and asset binding.
type CanonicalSegment struct {
	ID       string `json:"segment_id"`
	SceneID  string `json:"scene_id,omitempty"`
	Position int    `json:"position"`
	Text     string `json:"text"`
	TextHash string `json:"text_hash"`
}

// ExtractedEntity is a typed entity extracted from one segment.
type ExtractedEntity struct {
	Value      string  `json:"value"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// SegmentInsights collects the per-segment semantic extractions and
// generated queries used by VidRush.
type SegmentInsights struct {
	SegmentID        string            `json:"segment_id"`
	TextHash         string            `json:"text_hash"`
	Entities         []ExtractedEntity `json:"entities,omitempty"`
	ImportantPhrases []string          `json:"important_phrases,omitempty"`
	ImportantWords   []string          `json:"important_words,omitempty"`
	ArtlistQueries   []string          `json:"artlist_queries,omitempty"`
	ImageQueries     []string          `json:"image_queries,omitempty"`
}

// SegmentAssetCandidate is a single candidate found for a segment.
type SegmentAssetCandidate struct {
	AssetID          string  `json:"asset_id"`
	Provider         string  `json:"provider"`
	Query            string  `json:"query,omitempty"`
	Entity           string  `json:"entity,omitempty"`
	Score            float64 `json:"score"`
	SourceURL        string  `json:"source_url,omitempty"`
	SourcePageURL    string  `json:"source_page_url,omitempty"`
	PreviewURL       string  `json:"preview_url,omitempty"`
	DriveLink        string  `json:"drive_link,omitempty"`
	DurationMs       int64   `json:"duration_ms,omitempty"`
	Width            int     `json:"width,omitempty"`
	Height           int     `json:"height,omitempty"`
	RightsStatus     string  `json:"rights_status,omitempty"`
	SelectionReason  string  `json:"selection_reason,omitempty"`
	CandidateSetHash string  `json:"candidate_set_hash,omitempty"`
}

// SegmentAssetSelection is the winning asset bundle for a segment.
type SegmentAssetSelection struct {
	PrimaryVideo     *SegmentAssetCandidate  `json:"primary_video,omitempty"`
	SecondaryImages  []SegmentAssetCandidate `json:"secondary_images,omitempty"`
	Candidates       []SegmentAssetCandidate `json:"candidates,omitempty"`
	CandidateSetHash string                  `json:"candidate_set_hash,omitempty"`
	SelectionReason  string                  `json:"selection_reason,omitempty"`
}

// SegmentCacheState stores the per-segment cache status across the
// VidRush steps.
type SegmentCacheState struct {
	Extraction     string `json:"extraction,omitempty"`
	Artlist        string `json:"artlist,omitempty"`
	InternetImages string `json:"internet_images,omitempty"`
	Binding        string `json:"binding,omitempty"`
}

// VidRushSegmentResult is the full per-segment output surfaced by
// script generation.
type VidRushSegmentResult struct {
	SegmentID string                `json:"segment_id"`
	SceneID   string                `json:"scene_id,omitempty"`
	Position  int                   `json:"position"`
	Text      string                `json:"text"`
	TextHash  string                `json:"text_hash"`
	Insights  SegmentInsights       `json:"insights"`
	Assets    SegmentAssetSelection `json:"assets"`
	Cache     SegmentCacheState     `json:"cache"`
}

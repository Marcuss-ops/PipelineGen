package clipresolver

import (
	"context"
	"velox/go-master/internal/media/models"
)

// EmbeddingProvider defines the interface for obtaining text embeddings.
type EmbeddingProvider interface {
	EmbedText(ctx context.Context, text string) ([]float64, error)
}

// VectorStoreSearcher defines the interface for ANN vector search.
// Used as the primary search step before SQLite/FTS fallback.
type VectorStoreSearcher interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
}

// SearchRequest mirrors vectorstore.SearchRequest for the resolver's needs.
type SearchRequest struct {
	QueryVector []float32
	VectorName  string
	Limit       int
	MinScore    float64
	Source      string
	Category    string
	MediaType   string
}

// SearchResult mirrors vectorstore.SearchResult for the resolver's needs.
type SearchResult struct {
	AssetID   string
	Score     float64
	Source    string
	Name      string
	LocalPath string
	DriveLink string
	Category  string
	MediaType string
}

// OntologyScorer defines the interface for applying ontology-based scoring.
type OntologyScorer interface {
	Apply(score float64, clip *models.MediaAsset, topic string) float64
}

// RecommendRequest is the request for clip recommendation
type RecommendRequest struct {
	Topic         string   `json:"topic,omitempty"`
	SegmentID     string   `json:"segment_id,omitempty"`
	SegmentText   string   `json:"segment_text,omitempty"`
	Queries       []string `json:"queries"`
	EntityQueries []string `json:"entity_queries,omitempty"`
	VisualPrompts []string `json:"visual_prompts,omitempty"`
	Category      string   `json:"category,omitempty"`
	SceneType     string   `json:"scene_type,omitempty"`
	AvoidTerms    []string `json:"avoid_terms,omitempty"`
	UsedClipIDs   []string `json:"used_clip_ids,omitempty"`
	UsedFolderIDs []string `json:"used_folder_ids,omitempty"`
	UsedPaths     []string `json:"used_paths,omitempty"`
	Limit         int      `json:"limit"`
	MinScore      float64  `json:"min_score"`
	Explain       bool     `json:"explain"`
	AutoHarvest   bool     `json:"auto_harvest,omitempty"`
}

// RecommendResponse is the response for clip recommendation
type RecommendResponse struct {
	OK            bool              `json:"ok"`
	Topic         string            `json:"topic,omitempty"`
	SegmentID     string            `json:"segment_id,omitempty"`
	Recommended   []RecommendedClip `json:"recommended"`
	Rejected      []RejectedClip    `json:"rejected,omitempty"`
	NeedsHarvest  bool              `json:"needs_harvest"`
	HarvestTerms  []string          `json:"harvest_terms,omitempty"`
	HarvestJobIDs []string          `json:"harvest_job_ids,omitempty"`
}

// RecommendedClip represents a recommended clip with score breakdown
type RecommendedClip struct {
	ClipID         string          `json:"clip_id"`
	Title          string          `json:"title"`
	DriveLink      string          `json:"drive_link,omitempty"`
	LocalPath      string          `json:"local_path,omitempty"`
	FolderID       string          `json:"folder_id,omitempty"`
	FolderPath     string          `json:"folder_path,omitempty"`
	Score          float64         `json:"score"`
	MatchedQuery   string          `json:"matched_query,omitempty"`
	Category       string          `json:"category,omitempty"`
	SceneType      string          `json:"scene_type,omitempty"`
	MatchedTerms   []string        `json:"matched_terms,omitempty"`
	ScoreBreakdown *ScoreBreakdown `json:"score_breakdown,omitempty"`
	Reason         string          `json:"reason,omitempty"`
}

// RejectedClip represents a rejected clip with reason
type RejectedClip struct {
	ClipID       string  `json:"clip_id"`
	Title        string  `json:"title"`
	Score        float64 `json:"score"`
	RejectReason string  `json:"reject_reason"`
}

// ScoreBreakdown provides explainable scoring
type ScoreBreakdown struct {
	TextScore        float64 `json:"text_score,omitempty"`
	VectorScore      float64 `json:"vector_score,omitempty"`
	TopicBoost       float64 `json:"topic_boost,omitempty"`
	CategoryBoost    float64 `json:"category_boost,omitempty"`
	UsableForBoost   float64 `json:"usable_for_boost,omitempty"`
	SourceBoost      float64 `json:"source_boost,omitempty"`
	QualityScore     float64 `json:"quality_score,omitempty"`
	NegativePenalty  float64 `json:"negative_penalty,omitempty"`
	ReusePenalty     float64 `json:"reuse_penalty,omitempty"`
	DiversityPenalty float64 `json:"diversity_penalty,omitempty"`
}

// ClipScore is an internal type for scoring clips
type ClipScore struct {
	Clip         *models.MediaAsset
	Score        float64
	Breakdown    *ScoreBreakdown
	MatchedQuery string
	MatchedTerms []string
	RejectReason string
}

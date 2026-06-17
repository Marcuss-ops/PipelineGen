package handlers

import "velox/go-master/internal/service/scriptcore"

// GenerateFromCatalogRequest is the input for catalog-first script generation.
type GenerateFromCatalogRequest struct {
	Topic       string  `json:"topic" binding:"required"`
	MaxClips    int     `json:"max_clips"`
	MinCoverage float64 `json:"min_coverage"` // 0.0-1.0, minimum coverage score to proceed

	Title      string `json:"title"`
	OutputName string `json:"output_name,omitempty"`
	Language   string `json:"language,omitempty"`
	Tone       string `json:"tone,omitempty"`
	Model      string `json:"model,omitempty"`

	TargetWords      int    `json:"target_words,omitempty"`
	Duration         int    `json:"duration,omitempty"`
	TranscriptPolicy string `json:"transcript_policy,omitempty"`
	OrderingStrategy string `json:"ordering_strategy,omitempty"`

	CreateDoc        bool `json:"create_doc,omitempty"`
	SaveToDB         bool `json:"save_to_db,omitempty"`
	GenerateTimeline bool `json:"generate_timeline,omitempty"`
	ForceRefresh     bool `json:"force_refresh,omitempty"`

	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`
}

// GenerateFromCatalogResponse is the async job response.
type GenerateFromCatalogResponse struct {
	OK     bool   `json:"ok"`
	JobID  string `json:"job_id"`
	Status string `json:"status"`

	// CatalogReport is included when the scan completes synchronously
	// (before the job is enqueued), giving the user immediate visibility
	// into which clusters and clips were selected.
	CatalogReport *scriptcore.CatalogReport `json:"catalog_report,omitempty"`
}

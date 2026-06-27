package stockpipeline

// StockRunPayload is the job payload for media.stock jobs.
// It was previously in the now-deleted internal/core/jobs/payloads.go (PR4).
type StockRunPayload struct {
	SearchQueries []string                 `json:"search_queries"`
	DirectURLs    []string                 `json:"direct_urls,omitempty"`
	TotalMinutes  int                      `json:"total_minutes"`
	ChunkDuration int                      `json:"chunk_duration,omitempty"`
	ClipDuration  int                      `json:"clip_duration,omitempty"`
	NoAudio       bool                     `json:"no_audio,omitempty"`
	NoEffects     bool                     `json:"no_effects,omitempty"`
	NoTransitions bool                     `json:"no_transitions,omitempty"`
	MaxVideos     int                      `json:"max_videos,omitempty"`
	Subfolder     string                   `json:"subfolder"`
	FolderName    string                   `json:"folder_name"`
	FolderID      string                   `json:"folder_id,omitempty"`
	Metadata      *StockRunPayloadMetadata `json:"metadata,omitempty"`
	// Async is the submitter's wire-shape audit trail: true means the
	// operator asked for the jobs-broker path (canonical production);
	// false means the operator asked for in-process sync. The
	// HandleJob worker is reached via jobs.Enqueue on either path so
	// this field is informational for downstream telemetry, NOT a
	// worker reroute. Defaults to false (zero-value); the api handler
	// forces Async=true before JSON binding so existing clients
	// preserve the async-by-default production behaviour.
	Async bool `json:"async,omitempty"`
}

// StockRunPayloadMetadata mirrors ChunkMetadataInput for JSON compatibility.
type StockRunPayloadMetadata struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

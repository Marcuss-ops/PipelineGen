package main

import "time"

// SummaryResponse is the aggregated dashboard data from the PipelineGen API.
type SummaryResponse struct {
	TotalAssets   int64            `json:"total_assets"`
	BySource      map[string]int64 `json:"by_source"`
	ByMediaType   map[string]int64 `json:"by_media_type"`
	Indexed       int64            `json:"indexed"`
	NonIndexed    int64            `json:"non_indexed"`
	LocalCount    int64            `json:"local_count"`
	DriveCount    int64            `json:"drive_count"`
	OutboxPending int64            `json:"outbox_pending"`
	OutboxFailed  int64            `json:"outbox_failed"`
	OutboxRetry   int64            `json:"outbox_retry"`
	JobsRunning   int64            `json:"jobs_running"`
	JobsFailed    int64            `json:"jobs_failed"`
	JobsCompleted int64            `json:"jobs_completed"`
	LatestAssets  []AssetSummary   `json:"latest_assets"`
	LatestErrors  []JobSummary     `json:"latest_errors"`
}

// AssetSummary is a lightweight projection for list views.
type AssetSummary struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Name           string    `json:"name"`
	Filename       string    `json:"filename"`
	MediaType      string    `json:"media_type"`
	Category       string    `json:"category"`
	LifecycleState string    `json:"lifecycle_state"`
	Duration       string    `json:"duration"`
	Tags           []string  `json:"tags"`
	UpdatedAt      time.Time `json:"updated_at"`
	CreatedAt      time.Time `json:"created_at"`
	HasLocal       bool      `json:"has_local"`
	HasDrive       bool      `json:"has_drive"`
	IndexState     string    `json:"index_state"`
}

// AssetDetailResponse is the full representation of an asset.
type AssetDetailResponse struct {
	ID             string           `json:"id"`
	Source         string           `json:"source"`
	Name           string           `json:"name"`
	Filename       string           `json:"filename"`
	MediaType      string           `json:"media_type"`
	Category       string           `json:"category"`
	Group          string           `json:"group"`
	SourceURL      string           `json:"source_url"`
	ClipPageURL    string           `json:"clip_page_url"`
	ThumbnailURL   string           `json:"thumbnail_url"`
	Duration       string           `json:"duration"`
	Tags           []string         `json:"tags"`
	SearchTerms    []string         `json:"search_terms"`
	SearchText     string           `json:"search_text"`
	LifecycleState string           `json:"lifecycle_state"`
	Metadata       map[string]any   `json:"metadata"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	Locations      []LocationInfo   `json:"locations"`
	Processing     []ProcessingInfo `json:"processing"`
	Versions       []VersionInfo    `json:"versions"`
	EmbeddingInfo  EmbeddingInfo    `json:"embedding_info"`
}

// LocationInfo describes a single storage location for an asset.
type LocationInfo struct {
	Kind          string `json:"kind"`
	URI           string `json:"uri"`
	ExternalID    string `json:"external_id"`
	IsPrimary     bool   `json:"is_primary"`
	MimeType      string `json:"mime_type"`
	FileSizeBytes int64  `json:"file_size_bytes"`
	FileHash      string `json:"file_hash"`
}

// ProcessingInfo describes a processing step for an asset.
type ProcessingInfo struct {
	Step      string `json:"step"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
}

// VersionInfo describes an embedding version for an asset.
type VersionInfo struct {
	Version    string `json:"version"`
	Dimensions int    `json:"dimensions"`
	Model      string `json:"model"`
	CreatedAt  string `json:"created_at"`
}

// EmbeddingInfo is a safe summary of embedding state (no raw vectors).
type EmbeddingInfo struct {
	Present    bool   `json:"present"`
	Dimensions int    `json:"dimensions"`
	Version    string `json:"version"`
}

// JobSummary is a lightweight projection for job lists.
type JobSummary struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	Progress    int        `json:"progress"`
	RetryCount  int        `json:"retry_count"`
	Error       string     `json:"error"`
	Project     string     `json:"project"`
	WorkerID    string     `json:"worker_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// JobStatsResponse is the aggregated job statistics.
type JobStatsResponse struct {
	Stats map[string]int64 `json:"stats"`
}

// OutboxStatusResponse is the outbox event counts by status.
type OutboxStatusResponse struct {
	OK     bool             `json:"ok"`
	Counts map[string]int64 `json:"counts"`
}

// OutboxEvent is a single outbox event.
type OutboxEvent struct {
	ID          string     `json:"id"`
	EventType   string     `json:"event_type"`
	Status      string     `json:"status"`
	Payload     string     `json:"payload"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	NextAttempt *time.Time `json:"next_attempt_at"`
}

// OutboxEventsResponse wraps the outbox event list.
type OutboxEventsResponse struct {
	OK     bool          `json:"ok"`
	Events []OutboxEvent `json:"events"`
	Count  int           `json:"count"`
}

// DiagnosticsResponse wraps the diagnostics check result.
type DiagnosticsResponse struct {
	OK       bool           `json:"ok"`
	Degraded bool           `json:"degraded"`
	Checks   map[string]any `json:"checks"`
}

// IndexHealthResponse wraps the index health check result.
type IndexHealthResponse struct {
	OK          bool           `json:"ok"`
	Degraded    bool           `json:"degraded"`
	IndexHealth any            `json:"index_health"`
	AssetStats  map[string]any `json:"asset_stats"`
}

// AssetListResponse wraps a paginated asset list.
type AssetListResponse struct {
	Assets     []AssetSummary `json:"assets"`
	Count      int            `json:"count"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

// JobListResponse wraps a paginated job list.
type JobListResponse struct {
	Jobs  []JobSummary `json:"jobs"`
	Count int          `json:"count"`
}

// JobFullResponse wraps a full job with events.
type JobFullResponse struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"`
	Status        string      `json:"status"`
	Progress      int         `json:"progress"`
	Error         string      `json:"error"`
	CorrelationID string      `json:"correlation_id"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	StartedAt     *time.Time  `json:"started_at"`
	CompletedAt   *time.Time  `json:"completed_at"`
	Events        []JobEvent  `json:"events"`
	Timeline      []JobEvent  `json:"timeline"`
	Retryable     bool        `json:"retryable"`
	Job           *JobSummary `json:"job"`
}

// JobEvent is a single timeline event for a job.
type JobEvent struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
}

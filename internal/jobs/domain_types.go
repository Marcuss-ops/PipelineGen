// Package job defines the canonical domain types for the job system.
//
// These are the single source of truth for job status, filtering, and entity
// representation. The legacy models package (internal/media/models) still
// exists for backward-compat with the HTTP layer and will be migrated in
// Passaggio 6.
package jobs

import (
	"encoding/json"
	"time"
)

// Status is the canonical 7-state job lifecycle (PR4: SSOT).
//
//	queued     → waiting for a worker
//	leased     → worker has claimed the job (fencing token held)
//	running    → worker is executing the handler
//	retry_wait → job failed temporarily, waiting for retry backoff
//	completed  → finished successfully (terminal)
//	failed     → exhausted retries or non-retryable error (terminal)
//	cancelled  → operator cancelled (terminal)
//
// Allowed transitions:
//
//	queued     → leased, cancelled
//	leased     → running, queued (lease expiry), cancelled
//	running    → completed, failed, retry_wait, cancelled
//	retry_wait → queued, failed, cancelled
//	failed     → queued (retry)
//	completed  → (terminal)
//	cancelled  → (terminal)
type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusLeased    Status = "LEASED"
	StatusRunning   Status = "RUNNING"
	StatusRetryWait Status = "RETRY_WAIT"
	StatusSucceeded Status = "SUCCEEDED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

// Filter narrows job queries. All fields are optional; nil/zero means
// "don't filter".
type Filter struct {
	Status   *Status
	Type     *string
	WorkerID string
	Limit    int
	Offset   int
}

// ── Job type string constants (PR4: SSOT) ───────────────────────────

// Canonical job type identifiers for handler registration and job
// routing. These are the single source of truth; legacy
// models.JobType* constants will be removed.
const (
	JobTypeMediaExtract           = "media.extract"
	JobTypeMediaStock             = "media.stock"
	JobTypeVoiceoverBatch         = "voiceover.batch"
	JobTypeSubtitleGenerate       = "subtitle.generate"
	JobTypeRenderVideo            = "render.video"
	JobTypeYouTubeUpload          = "youtube.upload"
	JobTypeYouTubeClipExtract     = "youtube_clip.extract"
	JobTypeCatalogSync            = "catalog.sync"
	JobTypeArtlistRun             = "media.artlist"
	JobTypeSystemCleanup          = "system.cleanup"
	JobTypeMediaGenerate          = "media.generate_missing_asset"
	JobTypeVideoGenerate          = "video.generate"
	JobTypeBooksProcess           = "books.process"
	JobTypeLessonsProcess         = "lessons.process"
	JobTypeMediaReindex           = "media.reindex"
	JobTypeYouTubeRebuildST       = "youtube.rebuild_search_text"
	JobTypeBatchScriptGenerate    = "script.generate_batch"
	JobTypeClipScriptGenerate     = "script.generate_from_clips"
	JobTypeCatalogScriptGenerate  = "script.generate_from_catalog"
	JobTypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips"
	JobTypeDriveFolderSync        = "drive.folder.sync"
)

// Job is the canonical domain entity for a job in the system.
//
// Type is string — domain-agnostic, matched against the Dispatcher
// registry. Status is Status (the 7-state lifecycle). Result is
// json.RawMessage (not map[string]any). LeaseID/LeaseExpiry/Revision
// carry lease-fencing tokens for atomic state transitions.
type Job struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Status         Status          `json:"status"`
	Priority       int             `json:"priority"`
	Project        string          `json:"project,omitempty"`
	VideoName      string          `json:"video_name,omitempty"`
	ActiveKey      string          `json:"active_key,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          string          `json:"error,omitempty"`
	Progress       int             `json:"progress"`
	RetryCount     int             `json:"retry_count"`
	MaxRetries     int             `json:"max_retries"`
	WorkerID       string          `json:"worker_id,omitempty"`
	LeaseID        string          `json:"lease_id,omitempty"`
	LeaseExpiry    *time.Time      `json:"lease_expiry,omitempty"`
	Revision       int             `json:"revision"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	WorkflowID     string          `json:"workflow_id,omitempty"`
	WorkflowStepID string          `json:"workflow_step_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	CancelledAt    *time.Time      `json:"cancelled_at,omitempty"`
}

// IsTerminal and CanRetry are defined in functions.go.

// Event represents a discrete event in a job's timeline.
type Event struct {
	ID        string         `json:"id"`
	JobID     string         `json:"job_id"`
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

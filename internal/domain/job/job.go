// Package job defines the canonical domain types for the job system.
//
// These are the single source of truth for job status, filtering, and entity
// representation. Infrastructure implementations (SQLite store, dispatcher,
// worker) live in internal/application/jobs/ and internal/infrastructure/jobs/.
package job

import (
	"encoding/json"
	"time"
)

// Status is the canonical 7-state job lifecycle.
//
//	queued → leased → running → completed/failed/retry_wait/cancelled
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

// IsTerminal returns true if the status is a final state.
func (s Status) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// IsActive returns true if a worker currently owns this job.
func (s Status) IsActive() bool {
	return s == StatusLeased || s == StatusRunning
}

// Valid returns true if s is a known job status.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusLeased, StatusRunning, StatusRetryWait,
		StatusSucceeded, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Filter narrows job queries. All fields are optional; nil/zero means "don't filter".
type Filter struct {
	Status   *Status
	Type     *string
	WorkerID string
	Limit    int
	Offset   int
}

// ── Job type string constants (SSOT) ────────────────────────────────

const (
	TypeMediaExtract           = "media.extract"
	TypeMediaStock             = "media.stock"
	TypeVoiceoverBatch         = "voiceover.batch"
	TypeVoiceoverGenerate      = "voiceover.generate"
	TypeSubtitleGenerate       = "subtitle.generate"
	TypeRenderVideo            = "render.video"
	TypeYouTubeUpload          = "youtube.upload"
	TypeYouTubeClipExtract     = "youtube_clip.extract"
	TypeCatalogSync            = "catalog.sync"
	TypeArtlistRun             = "media.artlist"
	TypeSystemCleanup          = "system.cleanup"
	TypeMediaGenerate          = "media.generate_missing_asset"
	TypeVideoGenerate          = "video.generate"
	TypeBooksProcess           = "books.process"
	TypeLessonsProcess         = "lessons.process"
	TypeMediaReindex           = "media.reindex"
	TypeYouTubeRebuildST       = "youtube.rebuild_search_text"
	TypeScriptGenerate         = "script.generate"
	TypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips"
	TypeDriveFolderSync        = "drive.folder.sync"
	TypeMediaCurate            = "media.curate"
	// PR 5 (June 2026 — codex/clips-cleanup-job):
	// assets.cleanup is the canonical paginated async cleanup job. It
	// replaces the previous synchronous ListClipsPaged(..., 10000, 0, "")
	// scan in ClipOpsService.Cleanup with a batch=250 paginated loop
	// that persists its cursor in j.Payload cursor and emits progress
	// per batch. Resume is achieved by re-enqueueing with the same
	// ActiveKey (FindActiveByKey filter excludes terminal jobs).
	TypeAssetsCleanup = "assets.cleanup"
	// PR 3 (June 2026): voiceover.generate is the canonical typed job
	// for voiceover generation. It replaces voiceover.batch and
	// voiceover.promo which are now removed from the registry.
	TypeMediaEnrich    = "media.enrich"
	TypeVoiceoverPromo = "voiceover.promo"
)

// Job is the canonical domain entity for a job in the system.
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

// Event represents a discrete event in a job's timeline.
type Event struct {
	ID        string         `json:"id"`
	JobID     string         `json:"job_id"`
	Type      string         `json:"type"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// IsTerminal returns true if the job has reached a terminal state.
func (j *Job) IsTerminal() bool {
	if j == nil {
		return false
	}
	return j.Status.IsTerminal()
}

// CanRetry checks if the job can be retried.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusRetryWait)
}

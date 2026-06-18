package models

import (
	"encoding/json"
	"time"
)

type JobStatus string

// Canonical 7-state job lifecycle (PR-2: atomic lifecycle).
//
//   PENDING     → job is waiting for a worker
//   LEASED      → worker has claimed the job (fencing token)
//   RUNNING     → worker has started executing the job
//   SUCCEEDED   → job finished successfully (terminal)
//   RETRY_WAIT  → job failed temporarily, waiting for retry backoff
//   FAILED      → job exhausted retries or hit non-retryable error (terminal)
//   CANCELLED   → operator cancelled the job (terminal)
//
// Allowed transitions:
//   PENDING    → LEASED, CANCELLED
//   LEASED     → RUNNING, PENDING (lease expiry), CANCELLED
//   RUNNING    → SUCCEEDED, RETRY_WAIT, FAILED, CANCELLED
//   RETRY_WAIT → PENDING, FAILED, CANCELLED
const (
	StatusPending    JobStatus = "PENDING"
	StatusLeased     JobStatus = "LEASED"
	StatusRunning    JobStatus = "RUNNING"
	StatusSucceeded  JobStatus = "SUCCEEDED"
	StatusRetryWait  JobStatus = "RETRY_WAIT"
	StatusFailed     JobStatus = "FAILED"
	StatusCancelled  JobStatus = "CANCELLED"

	// Legacy aliases (will be removed once all callers migrate)
	StatusQueued    = StatusPending
	StatusCompleted = StatusSucceeded
)

type JobType string

const (
	JobTypeMediaExtract           JobType = "media.extract"
	JobTypeMediaStock             JobType = "media.stock"
	JobTypeVoiceoverBatch         JobType = "voiceover.batch"
	JobTypeSubtitleGenerate       JobType = "subtitle.generate"
	JobTypeRenderVideo            JobType = "render.video"
	JobTypeYouTubeUpload          JobType = "youtube.upload"
	JobTypeYouTubeClipExtract     JobType = "youtube_clip.extract"
	JobTypeCatalogSync            JobType = "catalog.sync"
	JobTypeArtlistRun             JobType = "media.artlist"
	JobTypeSystemCleanup          JobType = "system.cleanup"
	JobTypeMediaGenerate          JobType = "media.generate_missing_asset"
	JobTypeVideoGenerate          JobType = "video.generate"
	JobTypeBooksProcess           JobType = "books.process"
	JobTypeLessonsProcess         JobType = "lessons.process"
	JobTypeMediaReindex           JobType = "media.reindex"
	JobTypeYouTubeRebuildST       JobType = "youtube.rebuild_search_text"
	JobTypeBatchScriptGenerate    JobType = "script.generate_batch"
	JobTypeClipScriptGenerate     JobType = "script.generate_from_clips"
	JobTypeCatalogScriptGenerate  JobType = "script.generate_from_catalog"
	JobTypeBulkUploadYouTubeClips JobType = "media.bulk_upload_youtube_clips"
	JobTypeDriveFolderSync        JobType = "drive.folder.sync"
)

// Job rappresenta un job nel sistema
type Job struct {
	ID            string          `json:"id"`
	Type          JobType         `json:"type"`
	Status        JobStatus       `json:"status"`
	Priority      int             `json:"priority"`
	Project       string          `json:"project,omitempty"`
	VideoName     string          `json:"video_name,omitempty"`
	ActiveKey     string          `json:"active_key,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
	CancelledAt   *time.Time      `json:"cancelled_at,omitempty"`
	WorkerID      string          `json:"worker_id,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	Result        map[string]any  `json:"result,omitempty"`
	Error         string          `json:"error,omitempty"`
	RetryCount    int             `json:"retry_count"`
	MaxRetries    int             `json:"max_retries"`
	Progress      int             `json:"progress"`
	LeaseExpiry   *time.Time      `json:"lease_expiry,omitempty"`
	// Revision is a per-row monotonic counter incremented on every status
	// transition. It's the optimistic-lock token used by the Repository —
	// passed on read, verified on write.
	Revision int `json:"revision"`
	// LeaseID is the fencing token assigned by ClaimNext. It must match
	// on all worker-originated operations (Start, RenewLease, Complete,
	// Fail, ScheduleRetry, ConfirmCancelled).
	LeaseID string `json:"lease_id,omitempty"`
}

// CreateJobRequest richiesta per creare un nuovo job
type CreateJobRequest struct {
	Type       JobType         `json:"type"`
	Project    string          `json:"project"`
	VideoName  string          `json:"video_name,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Priority   int             `json:"priority,omitempty"`
	MaxRetries int             `json:"max_retries,omitempty"`
}

// JobEvent rappresenta un evento del job
type JobEvent struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// JobResult contiene il risultato di un job completato
type JobResult struct {
	Success     bool            `json:"success"`
	OutputPath  string          `json:"output_path,omitempty"`
	VideoURL    string          `json:"video_url,omitempty"`
	DriveFileID string          `json:"drive_file_id,omitempty"`
	YouTubeID   string          `json:"youtube_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CompletedAt time.Time       `json:"completed_at"`
}

// Queue rappresenta la coda dei job
type Queue struct {
	Jobs      []*Job    `json:"jobs"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

// JobFilter rappresenta i filtri per la ricerca dei job
type JobFilter struct {
	Status   *JobStatus
	Type     *JobType
	WorkerID string
	Limit    int
	Offset   int
}

// IsTerminal returns true if the status is one of the three terminal states.
func (s JobStatus) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// IsActive returns true if a worker currently owns this job.
func (s JobStatus) IsActive() bool {
	return s == StatusLeased || s == StatusRunning
}

// CanRetry checks if the job can be retried.
func (j *Job) CanRetry() bool {
	if j == nil {
		return false
	}
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusRetryWait)
}

package models

import (
	"encoding/json"
	"time"
)

type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusQueued     JobStatus = "queued"
	StatusProcessing JobStatus = "processing"
	StatusRunning    JobStatus = "running"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
	StatusPaused     JobStatus = "paused"
	StatusCancelled  JobStatus = "cancelled"
	StatusZombie     JobStatus = "zombie"
	StatusRetrying   JobStatus = "retrying"
)

type JobType string

const (
	JobTypeMediaExtract       JobType = "media.extract"
	JobTypeMediaStock         JobType = "media.stock"
	JobTypeVoiceoverBatch     JobType = "voiceover.batch"
	JobTypeSubtitleGenerate   JobType = "subtitle.generate"
	JobTypeRenderVideo        JobType = "render.video"
	JobTypeYouTubeUpload      JobType = "youtube.upload"
	JobTypeYouTubeClipExtract JobType = "youtube_clip.extract"
	JobTypeCatalogSync        JobType = "catalog.sync"
	JobTypeArtlistRun         JobType = "media.artlist"
	JobTypeContentPackage     JobType = "content.package"
	JobTypeSystemCleanup      JobType = "system.cleanup"
)

// Job rappresenta un job nel sistema
type Job struct {
	ID          string                 `json:"id"`
	Type        JobType                `json:"type"`
	Status      JobStatus              `json:"status"`
	Priority    int                    `json:"priority"`
	Project     string                 `json:"project,omitempty"`
	VideoName   string                 `json:"video_name,omitempty"`
	ActiveKey   string                 `json:"active_key,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	CancelledAt *time.Time             `json:"cancelled_at,omitempty"`
	WorkerID    string                 `json:"worker_id,omitempty"`
	Payload     json.RawMessage        `json:"payload,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	RetryCount  int                    `json:"retry_count"`
	MaxRetries  int                    `json:"max_retries"`
	Progress    int                    `json:"progress"`
	LeaseExpiry *time.Time             `json:"lease_expiry,omitempty"`
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

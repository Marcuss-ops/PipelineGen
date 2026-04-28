package models

import (
	"time"
)

// JobStatus rappresenta lo stato di un job
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

const (
	JobTypeVideoGeneration   = TypeVideoGeneration
	JobTypeAudioProcessing   = TypeAudioProcessing
	JobTypeUpload            = TypeUpload
	JobTypeVoiceover         = TypeVoiceover
	JobTypeScript            = TypeScriptGen
	JobTypeStockDownload     = TypeStockDownload
	JobTypeStockClip         = TypeStockClip
)

// JobType rappresenta il tipo di job
type JobType string

const (
	TypeVideoGeneration JobType = "video_generation"
	TypeAudioProcessing JobType = "audio_processing"
	TypeUpload          JobType = "upload"
	TypeScriptGen       JobType = "script_generation"
	TypeVoiceover       JobType = "voiceover"
	TypeStockDownload   JobType = "stock_download"
	TypeStockClip       JobType = "stock_clip"
)

// Job rappresenta un job nel sistema
type Job struct {
	ID           string                 `json:"id"`
	Type         JobType                `json:"type"`
	Status       JobStatus              `json:"status"`
	Priority     int                    `json:"priority"`
	Project      string                 `json:"project,omitempty"`
	VideoName    string                 `json:"video_name,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	StartedAt    *time.Time             `json:"started_at,omitempty"`
	CompletedAt  *time.Time             `json:"completed_at,omitempty"`
	WorkerID     string                 `json:"worker_id,omitempty"`
	Payload      map[string]interface{} `json:"payload"`
	Result       map[string]interface{} `json:"result,omitempty"`
	Error        string                 `json:"error,omitempty"`
	Retries      int                    `json:"retries"`
	RetryCount   int                    `json:"retry_count"`
	MaxRetries   int                    `json:"max_retries"`
	Progress     int                    `json:"progress"`
	LeaseExpiry  *time.Time             `json:"lease_expiry,omitempty"`
}

// CreateJobRequest richiesta per creare un nuovo job
type CreateJobRequest struct {
	Type       JobType                `json:"type"`
	Project    string                 `json:"project"`
	VideoName  string                 `json:"video_name,omitempty"`
	Payload    map[string]interface{} `json:"payload"`
	Priority   int                    `json:"priority,omitempty"`
	MaxRetries int                    `json:"max_retries,omitempty"`
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
	Success      bool                   `json:"success"`
	OutputPath   string                 `json:"output_path,omitempty"`
	VideoURL     string                 `json:"video_url,omitempty"`
	DriveFileID  string                 `json:"drive_file_id,omitempty"`
	YouTubeID    string                 `json:"youtube_id,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	CompletedAt  time.Time              `json:"completed_at"`
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
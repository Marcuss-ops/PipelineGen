package googleaccounting

import (
	"time"
)

// JobStatus represents the state of a Google Accounting job
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusDone      JobStatus = "done"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
)

// Job represents a background task in the Google Accounting service
type Job struct {
	JobID       string    `json:"job_id"`
	Status      JobStatus `json:"status"`
	Progress    int       `json:"progress"`
	CurrentStep string    `json:"current_step"`
	Attempts    int       `json:"attempts"`
	LastLog     string    `json:"last_log"`
	Error       string    `json:"error,omitempty"`
	FilePath    string    `json:"file_path,omitempty"`
	Files       []string  `json:"files,omitempty"`
	CreatedAt   float64   `json:"created_at"`
	UpdatedAt   float64   `json:"updated_at"`
}

// GenerateRequest is the shared request structure for video generation
type GenerateRequest struct {
	VideoID  string `json:"video_id,omitempty"`
	Prompt   string `json:"prompt"`
	Style    string `json:"style,omitempty"`
	Headless *bool  `json:"headless,omitempty"`
	Account  string `json:"account,omitempty"`
}

// AvatarRequest is the shared request structure for AI Avatar generation
type AvatarRequest struct {
	VideoID  string `json:"video_id,omitempty"`
	Script   string `json:"script"`
	AvatarID string `json:"avatar_id,omitempty"`
	Headless *bool  `json:"headless,omitempty"`
	Account  string `json:"account,omitempty"`
}

// FlowImageRequest is the shared request structure for Flow image generation
type FlowImageRequest struct {
	Prompt    string `json:"prompt"`
	ProjectID string `json:"project_id,omitempty"`
	Style     string `json:"style,omitempty"`
	Headless  *bool  `json:"headless,omitempty"`
	Account   string `json:"account,omitempty"`
}

// VidsImageRequest is the shared request structure for Vids image generation
type VidsImageRequest struct {
	VideoID       string `json:"video_id,omitempty"`
	Prompt        string `json:"prompt"`
	Style         string `json:"style,omitempty"`
	Headless      *bool  `json:"headless,omitempty"`
	Account       string `json:"account,omitempty"`
	DriveFolderID string `json:"drive_folder_id,omitempty"`
}

// DownloadRequest is the shared request structure for asset downloads
type DownloadRequest struct {
	VideoID  string `json:"video_id"`
	FileType string `json:"file_type"` // "video", "image", "all"
	Headless *bool  `json:"headless,omitempty"`
	Account  string `json:"account,omitempty"`
}

// StartResponse is the initial response when a job is started
type StartResponse struct {
	JobID  string    `json:"job_id"`
	Status JobStatus `json:"status"`
	Error  string    `json:"error,omitempty"`
}

// Use standard time for Go-side processing if needed
func (j *Job) GetCreatedAt() time.Time {
	return time.Unix(int64(j.CreatedAt), 0)
}

func (j *Job) GetUpdatedAt() time.Time {
	return time.Unix(int64(j.UpdatedAt), 0)
}

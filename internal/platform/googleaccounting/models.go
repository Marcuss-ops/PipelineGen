package googleaccounting

import (
	"strings"
	"time"
)

// JobStatus represents the state of a remote image-generation job.
// The remote provider uses its own taxonomy (succeeded/done); we normalise
// on ingest.
type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded" // remote provider status
	StatusFailed    JobStatus = "failed"
)

// Job represents a background task in the Google Accounting service
type Job struct {
	ID          string                 `json:"id,omitempty"`
	JobID       string                 `json:"job_id,omitempty"`
	Status      JobStatus              `json:"status"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Progress    int                    `json:"progress,omitempty"`
	CurrentStep string                 `json:"current_step,omitempty"`
	Attempts    int                    `json:"attempts,omitempty"`
	LastLog     string                 `json:"last_log,omitempty"`
	FilePath    string                 `json:"file_path,omitempty"`
	Files       []string               `json:"files,omitempty"`
	CreatedAt   float64                `json:"created_at,omitempty"`
	UpdatedAt   float64                `json:"updated_at,omitempty"`
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
	DriveFolderID string `json:"drive_folder_id"`
	Isolated      *bool  `json:"isolated,omitempty"`
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

// Identifier returns the most useful job identifier for either local or remote APIs.
func (j Job) Identifier() string {
	if strings.TrimSpace(j.ID) != "" {
		return j.ID
	}
	return j.JobID
}

// IsTerminal reports whether the job is in a terminal state.
func (s JobStatus) IsTerminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed:
		return true
	}
	return false
}

// Use standard time for Go-side processing if needed
func (j *Job) GetCreatedAt() time.Time {
	return time.Unix(int64(j.CreatedAt), 0)
}

func (j *Job) GetUpdatedAt() time.Time {
	return time.Unix(int64(j.UpdatedAt), 0)
}

package jobs

import "time"

type JobType string

const (
	JobTypeArtlistRun         JobType = "artlist.run"
	JobTypeYouTubeClipExtract JobType = "youtube_clip.extract"
	JobTypeScriptGenerate     JobType = "script.generate"
	JobTypeScriptPublish      JobType = "script.publish"
	JobTypeVoiceoverGenerate  JobType = "voiceover.generate"
	JobTypeMediaMatch         JobType = "media.match"
	JobTypeMediaImport        JobType = "media.import"
	JobTypeMediaStock         JobType = "media.stock"
	JobTypeWorkflowRun        JobType = "workflow.run"
)

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusQueued     JobStatus = "queued"
	JobStatusProcessing JobStatus = "processing"
	JobStatusRunning    JobStatus = "running"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusCancelled  JobStatus = "cancelled"
	JobStatusPaused     JobStatus = "paused"
	JobStatusZombie     JobStatus = "zombie"
	JobStatusRetrying   JobStatus = "retrying"
)

func (s JobStatus) Valid() bool {
	switch s {
	case JobStatusPending, JobStatusQueued, JobStatusProcessing, JobStatusRunning,
		JobStatusCompleted, JobStatusFailed, JobStatusCancelled, JobStatusPaused,
		JobStatusZombie, JobStatusRetrying:
		return true
	default:
		return false
	}
}

func (s JobStatus) IsTerminal() bool {
	return s == JobStatusCompleted || s == JobStatusFailed || s == JobStatusCancelled
}

type Job struct {
	ID          string
	Type        JobType
	Status      JobStatus
	Payload     string
	Result      string
	Error       string
	Progress    int
	RetryCount  int
	MaxRetries  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}


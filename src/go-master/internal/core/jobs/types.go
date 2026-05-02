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
)

type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

func (s JobStatus) Valid() bool {
	switch s {
	case JobStatusPending, JobStatusRunning, JobStatusCompleted, JobStatusFailed, JobStatusCancelled:
		return true
	default:
		return false
	}
}

type Job struct {
	ID        string
	Type      JobType
	Status    JobStatus
	Payload   string
	Result    string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

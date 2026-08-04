package job

import "sort"

// StageName identifies one explicit generation stage. The stage contract is
// shared by parent and child jobs so progress is derived from child outcomes,
// not from a synthetic percentage.
type StageName string

const (
	StageScript      StageName = "script"
	StageTranslation StageName = "translation"
	StageVoiceover   StageName = "voiceover"
	StageUpload      StageName = "upload"
)

type StageStatus string

const (
	StageQueued    StageStatus = "queued"
	StageRunning   StageStatus = "running"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
	StageSkipped   StageStatus = "skipped"
)

// StageLanguageStatus is the durable observation emitted by one child job.
type StageLanguageStatus struct {
	Stage    StageName   `json:"stage"`
	Language string      `json:"language"`
	Status   StageStatus `json:"status"`
	JobID    string      `json:"job_id,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// StageProgress is the parent-facing aggregate for one stage.
type StageProgress struct {
	Stage     StageName             `json:"stage"`
	Completed int                   `json:"completed"`
	Total     int                   `json:"total"`
	Languages []StageLanguageStatus `json:"languages,omitempty"`
}

// AggregateStageProgressByStage groups child observations by stage while
// preserving input order. Total counts every observed child; Completed counts
// only terminal successful children.
func AggregateStageProgressByStage(statuses []StageLanguageStatus) map[string]StageProgress {
	out := make(map[string]StageProgress)
	for _, status := range statuses {
		if status.Stage == "" {
			continue
		}
		key := string(status.Stage)
		progress := out[key]
		if progress.Stage == "" {
			progress.Stage = status.Stage
		}
		progress.Total++
		if status.Status == StageCompleted {
			progress.Completed++
		}
		progress.Languages = append(progress.Languages, status)
		out[key] = progress
	}
	return out
}

// FlattenStageProgress gives stable order to persisted progress maps.
func FlattenStageProgress(progress map[string]StageProgress) []StageLanguageStatus {
	ordered := []StageName{StageScript, StageTranslation, StageVoiceover, StageUpload}
	out := make([]StageLanguageStatus, 0)
	seen := make(map[string]struct{}, len(progress))
	for _, stage := range ordered {
		if item, ok := progress[string(stage)]; ok {
			out = append(out, item.Languages...)
			seen[string(stage)] = struct{}{}
		}
	}
	unknown := make([]string, 0, len(progress))
	for key := range progress {
		if _, ok := seen[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		out = append(out, progress[key].Languages...)
	}
	return out
}

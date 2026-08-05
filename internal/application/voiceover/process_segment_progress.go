package voiceover

import job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

// setFinalStageProgress derives terminal counters from the canonical result
// fields. It is called at the shared pipeline boundary so both batch and child
// voiceover jobs expose the same phase/language contract.
func setFinalStageProgress(out *VoiceoverItemResult, language, jobID string) {
	if out == nil {
		return
	}
	stages := make(map[string]job.StageProgress)
	defer func() { out.StageProgress = stages }()
	add := func(stage job.StageName, status job.StageStatus, errText string) {
		stages[string(stage)] = job.StageProgress{
			Stage: stage,
			Completed: func() int {
				if status == job.StageCompleted {
					return 1
				}
				return 0
			}(),
			Total: 1,
			Languages: []job.StageLanguageStatus{{
				Stage: stage, Language: language, Status: status, JobID: jobID, Error: errText,
			}},
		}
	}

	errText := out.Error
	switch out.ErrorCode {
	case string(FailureMissingFolder), VoiceoverDestinationUnavailableCode:
		return
	case string(FailureTTS), string(FailureAudioPost), string(FailureNoLocalPayload):
		add(job.StageVoiceover, job.StageFailed, errText)
		return
	default:
		add(job.StageVoiceover, job.StageCompleted, "")
	}

	switch out.ErrorCode {
	case string(FailureUpload), string(FailureDestinationMismatch):
		add(job.StageUpload, job.StageFailed, errText)
		return
	default:
		if out.DriveFileID != "" {
			add(job.StageUpload, job.StageCompleted, "")
		}
	}

	if out.Status == StatusCompleted {
		add(job.StagePersistence, job.StageCompleted, "")
	} else if out.DriveFileID != "" {
		add(job.StagePersistence, job.StageFailed, errText)
	}
}

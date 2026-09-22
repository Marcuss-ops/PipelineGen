package adapters

import (
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stageForProcessor is the canonical mapping from a postprocessor to the
// workflow stage it reports to the parent job. It is the producer side of
// job.CanonicalStageOrder(): every stage listed there must be produced here,
// and a stage with no producer must not be listed there (godlike/07
// no-fake-availability).
//
// Exactly ONE processor is mapped per non-language stage. Two processors
// mapped onto the same stage would upsert the same (language, unit, job_id)
// observation, so the later one would silently overwrite the earlier one's
// failure with its own success — the mapping would then report the last
// processor to run, not whether the stage actually completed.
func stageForProcessor(name ProcessorName) job.StageName {
	switch name {
	case ProcessorClipBindings:
		return job.StageClips
	case ProcessorStockBindings:
		return job.StageStock
	case ProcessorTranslation:
		return job.StageTranslation
	case ProcessorVoiceover:
		return job.StageVoiceover
	case ProcessorVisualSlots:
		return job.StageOverlay
	case ProcessorPersistence:
		return job.StagePersistence
	case ProcessorDocument:
		return job.StageUpload
	default:
		return ""
	}
}

func recordProcessorProgress(result *PipelineResult, name ProcessorName, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, status job.StageStatus, jobID, errMsg string) {
	if result == nil {
		return
	}
	stage := stageForProcessor(name)
	if stage == "" {
		return
	}
	language := strings.TrimSpace(input.EffectiveLanguage)
	if language == "" && plan != nil {
		language = strings.TrimSpace(plan.Language)
	}
	if result.StageProgress == nil {
		result.StageProgress = make(map[string]job.StageProgress)
	}
	progress := result.StageProgress[string(stage)]
	progress.Stage = stage
	observation := job.StageLanguageStatus{
		Stage: stage, Language: language, Status: status, JobID: jobID, Error: errMsg,
	}
	found := false
	for i := range progress.Languages {
		if progress.Languages[i].Language == language &&
			progress.Languages[i].Unit == "" &&
			(jobID == "" || progress.Languages[i].JobID == jobID) {
			progress.Languages[i] = observation
			found = true
			break
		}
	}
	if !found {
		progress.Languages = append(progress.Languages, observation)
	}
	progress.Total = len(progress.Languages)
	progress.Completed = 0
	for _, item := range progress.Languages {
		if item.Status == job.StageCompleted {
			progress.Completed++
		}
	}
	result.StageProgress[string(stage)] = progress
}

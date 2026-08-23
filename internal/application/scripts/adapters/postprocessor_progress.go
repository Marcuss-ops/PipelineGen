package adapters

import (
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func stageForProcessor(name ProcessorName) job.StageName {
	switch name {
	case ProcessorTranslation:
		return job.StageTranslation
	case ProcessorVoiceover:
		return job.StageVoiceover
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
		if progress.Languages[i].Language == language && (jobID == "" || progress.Languages[i].JobID == jobID) {
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

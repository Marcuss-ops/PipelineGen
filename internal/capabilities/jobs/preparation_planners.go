package jobs

import (
	"context"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// noopPreparationPlanner is the safe default adapter for a registered job
// type whose concrete preparation DAG has not been supplied yet. Registration
// remains explicit and type-keyed; no job-type switch is introduced.
type noopPreparationPlanner struct{}

func (noopPreparationPlanner) Plan(_ context.Context, j *job.Job) (PreparationPlan, error) {
	return PreparationPlan{JobID: j.ID, Units: []PreparationUnit{}}, nil
}

// ComposeJobPreparationRegistry wires one planner entry per currently known
// preparation-capable job type. Concrete planners can replace these entries
// at the composition root before Freeze; runtime consumers only resolve by
// registry lookup.
func ComposeJobPreparationRegistry() (*JobPreparationRegistry, error) {
	registry := NewJobPreparationRegistry()
	scriptPlanner := NewScriptPreparationPlanner()
	planner := noopPreparationPlanner{}
	for _, jobType := range []string{
		job.TypeScriptGenerate,
		job.TypeScriptGenerateItem,
		job.TypeVoiceoverGenerate,
		job.TypeVoiceoverBatch,
		job.TypeVoiceoverGenerateItem,
		job.TypeVoiceoverPromo,
		job.TypeYouTubeClipExtract,
		job.TypeClipRender,
	} {
		if jobType == job.TypeScriptGenerate || jobType == job.TypeScriptGenerateItem {
			if err := RegisterPreparationPlanner(registry, jobType, scriptPlanner); err != nil {
				return nil, err
			}
			continue
		}
		if jobType == job.TypeClipRender {
			if err := RegisterPreparationPlanner(registry, jobType, NewClipPreparationPlanner()); err != nil {
				return nil, err
			}
			continue
		}
		if err := RegisterPreparationPlanner(registry, jobType, planner); err != nil {
			return nil, err
		}
	}
	registry.Freeze()
	return registry, nil
}

// Package scripts — generation_dispatcher.go owns the canonical
// routing decision for script.generate jobs (PR-GODOBJ-4 KILL list,
// July 2026). It is the ONLY place allowed to decide whether a
// one-item envelope runs through the single-item executor and a
// multi-item envelope runs through the batch executor.
//
// The dispatcher is intentionally thin: it checks the envelope shape
// and delegates. All execution policy (progress tracking, persistence,
// fan-out) lives in the respective executors.
package jobs

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GenerationDispatcher routes a decoded GenerationEnvelopeV2 to the
// appropriate executor.
type GenerationDispatcher interface {
	Dispatch(
		ctx context.Context,
		j *job.Job,
		env *domainScript.GenerationEnvelopeV2,
		tools *appjobs.JobTools,
	) (map[string]any, error)
}

// generationDispatcher is the production implementation of
// GenerationDispatcher.
type generationDispatcher struct {
	single SingleGenerationExecutor
	batch  BatchGenerationExecutor
}

// NewGenerationDispatcher constructs a GenerationDispatcher from the
// single and batch executors. Either executor may be nil; Dispatch
// will fail-closed at runtime rather than panic.
func NewGenerationDispatcher(single SingleGenerationExecutor, batch BatchGenerationExecutor) GenerationDispatcher {
	return &generationDispatcher{
		single: single,
		batch:  batch,
	}
}

// Dispatch routes the envelope to the single executor when it contains
// exactly one item, otherwise to the batch executor.
func (d *generationDispatcher) Dispatch(
	ctx context.Context,
	j *job.Job,
	env *domainScript.GenerationEnvelopeV2,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if d == nil {
		return nil, fmt.Errorf("generation dispatcher: not constructed")
	}
	if len(env.Items) == 1 {
		if d.single == nil {
			return nil, fmt.Errorf("generation dispatcher: single executor not configured")
		}
		return d.single.Execute(ctx, j, env, tools)
	}
	if d.batch == nil {
		return nil, fmt.Errorf("generation dispatcher: batch executor not configured")
	}
	return d.batch.Execute(ctx, j, env, tools)
}

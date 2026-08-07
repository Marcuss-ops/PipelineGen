// Package stockpipeline — orchestrator_stage_snapshots.go.
//
// Owns the read-only stage projection returned with a stock job result
// (StageSnapshot collection). Extracted from orchestrator_run.go to keep
// the orchestration entry point under the max_lines_per_file_strict gate.
package stockpipeline

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
)

// stageSnapshots projects the latest per-step rows from the step store
// into the stable, read-only stage list returned with a stock job result.
// A skipped stage is explicitly non-applicable rather than a false
// completed success (for example, compose_chunks is bypassed when the
// cutter output is already the canonical final artifact).
func (o *Orchestrator) stageSnapshots(ctx context.Context, input *RunInput) ([]StageSnapshot, error) {
	if o == nil || o.stepStore == nil {
		return nil, steps.ErrStoreNotWired
	}
	history, err := o.stepStore.ListByJob(ctx, o.cfg.JobId)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]steps.StepState, len(history))
	for _, row := range history {
		if existing, ok := latest[row.StepKey]; !ok || row.ID > existing.ID {
			latest[row.StepKey] = row
		}
	}
	stageNames := []string{
		StepKeyStockPlan,
		StepKeyStockStageSources,
		StepKeyStockExtractClips,
		StepKeyStockComposeChunks,
		StepKeyStockPublish,
		StepKeyStockFinalize,
	}
	stages := make([]StageSnapshot, 0, len(stageNames))
	for _, name := range stageNames {
		stage := StageSnapshot{Name: name, Status: string(steps.StatusPending), Applicable: true}
		bypassed := name == StepKeyStockComposeChunks && shouldBypassStockCompose(name, input)
		if bypassed {
			stage.Status = "skipped"
			stage.Applicable = false
		}
		if row, ok := latest[name]; ok && !bypassed {
			stage.Status = string(row.Status)
			stage.Attempt = row.Attempt
			if !row.StartedAt.IsZero() {
				startedAt := row.StartedAt
				stage.StartedAt = &startedAt
			}
			if !row.CompletedAt.IsZero() {
				completedAt := row.CompletedAt
				stage.CompletedAt = &completedAt
			}
			stage.LastError = row.LastError
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

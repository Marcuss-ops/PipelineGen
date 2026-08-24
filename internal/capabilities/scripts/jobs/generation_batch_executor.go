// Package scripts — generation_batch_executor.go owns the
// multi-item script.generate fan-out path (PR-GODOBJ-4 KILL list,
// July 2026). It is invoked by the GenerationDispatcher when the
// envelope contains zero or more than one item.
//
// Responsibilities:
//   - pipeline context cancellation check at multi-item boundary
//   - progress + event emission
//   - dispatch to GenerateManyUseCase.ExecuteFanout
//   - build the parent waiting_children result map with the canonical
//     empty artifact manifest
//
// The executor returns a (map[string]any, error) pair so the worker
// broker can route FAILED vs COMPLETED correctly (godlike/07
// no-fake-availability).
package jobs

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// BatchGenerationExecutor is the narrow port for multi-item script
// generation fan-out. The production implementation is
// batchGenerationExecutor; tests may inject stubs.
type BatchGenerationExecutor interface {
	Execute(
		ctx context.Context,
		j *job.Job,
		env *domainScript.GenerationEnvelopeV2,
		tools *appjobs.JobTools,
	) (map[string]any, error)
}

// batchGenerationExecutor implements BatchGenerationExecutor using
// the canonical GenerateManyUseCase.
type batchGenerationExecutor struct {
	many *usecase.GenerateManyUseCase
	log  *zap.Logger
}

// NewBatchGenerationExecutor constructs the canonical
// BatchGenerationExecutor. many may be nil; Execute will fail-closed
// at runtime rather than panic.
func NewBatchGenerationExecutor(many *usecase.GenerateManyUseCase, log *zap.Logger) BatchGenerationExecutor {
	return &batchGenerationExecutor{
		many: many,
		log:  log,
	}
}

// Execute fans out each item as a separate script.generate_item
// child job via the wired broker. It preserves the exact behavior
// of the former GenerateJobHandler.handleBatch /
// handleBatchFanout methods.
func (e *batchGenerationExecutor) Execute(
	ctx context.Context,
	j *job.Job,
	env *domainScript.GenerationEnvelopeV2,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if e == nil {
		return nil, fmt.Errorf("batch generation executor: not constructed")
	}
	if e.many == nil {
		return nil, fmt.Errorf("batch generation executor: GenerateManyUseCase not configured")
	}
	if err := checkPipelineCtx(ctx, e.log, "multi-item-pre-execute"); err != nil {
		return nil, err
	}

	progressFn := appjobs.SafeProgressFn(tools)
	eventFn := appjobs.SafeEventFn(tools)

	eventFn("job.created", "Script generation batch job created", map[string]any{
		"job_id":     j.ID,
		"item_count": len(env.Items),
		"preset":     string(env.Preset),
	})

	progressFn(5, "fanning out script items to child jobs")

	fanout, err := e.many.ExecuteFanout(ctx, j.ID, env)
	if err != nil {
		if e.log != nil {
			e.log.Error("script.generate: fanout failed",
				zap.String("job_id", j.ID),
				zap.Error(err))
		}
		eventFn("job.failed", "Script generation batch failed", map[string]any{
			"job_id": j.ID,
			"error":  err.Error(),
		})
		return nil, fmt.Errorf("script.generate fanout: %w", err)
	}

	if fanout == nil {
		return nil, fmt.Errorf("script.generate fanout: ExecuteFanout returned nil result without error")
	}

	stageStatuses := make([]job.StageLanguageStatus, 0, fanout.TotalItems)
	for i, language := range fanout.PerLanguage {
		status := job.StageQueued
		errorText := ""
		childJobID := ""
		if i < len(fanout.ChildJobIDs) {
			childJobID = fanout.ChildJobIDs[i]
		}
		if childJobID == "" {
			status = job.StageFailed
			errorText = "script child job was not enqueued"
		}
		stageStatuses = append(stageStatuses, job.StageLanguageStatus{
			Stage: job.StageScript, Language: language, Status: status,
			JobID: childJobID, Error: errorText,
		})
	}

	resultMap := map[string]any{
		"parent_state":         "waiting_children",
		"parent_job_id":        j.ID,
		"total_items":          fanout.TotalItems,
		"child_job_ids":        fanout.ChildJobIDs,
		"per_language":         fanout.PerLanguage,
		"stage_progress":       job.AggregateStageProgressByStage(stageStatuses),
		"failed_enqueue_count": fanout.FailedEnqueueCount,
		// The batch parent owns no local artifact files; its children do.
		// Emit the canonical empty manifest so the artifact-producing worker
		// path can complete the waiting parent and let the aggregator finalize
		// it after all children terminate.
		job.ManifestKey: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			WorkflowID:    j.ID,
			JobID:         j.ID,
			Artifacts:     []job.Artifact{},
		},
	}

	if e.log != nil {
		e.log.Info("script.generate: fanout complete, parent waiting for children",
			zap.String("parent_job_id", j.ID),
			zap.Int("total_items", fanout.TotalItems),
			zap.Int("children_enqueued", fanout.TotalEnqueued),
			zap.Int("failed_enqueue", fanout.FailedEnqueueCount))
	}

	progressFn(100, "fanout complete, waiting for child aggregation")
	return resultMap, nil
}

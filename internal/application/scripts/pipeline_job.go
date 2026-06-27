package scripts

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

func (pu *PipelineUseCase) RegisterJobs(jobsSvc Broker) error {
	if pu == nil {
		return fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}
	if jobsSvc == nil {
		return nil
	}
	if err := jobsSvc.RegisterHandler(job.TypeClipScriptGenerate, pu.HandleJob); err != nil {
		return fmt.Errorf("pipeline: register handler: %w", err)
	}
	if pu.log != nil {
		pu.log.Info("registered script.generate_from_clips job handler")
	}
	return nil
}

// HandleJob is the canonical job-system entry point. Acquires the
// sem slot, kicks off the prewarm goroutine (if applicable), and
// delegates to Run. The worker receives the typed error chain so
// the job system can classify failures (permanent vs retryable).
func (pu *PipelineUseCase) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if pu == nil {
		return nil, fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}
	if pu.log != nil {
		pu.log.Info("handling unified script generation job", zap.String("job_id", j.ID))
	}

	if pu.semUC != nil {
		release, err := pu.semUC.Acquire(ctx, j.ID)
		if err != nil {
			return nil, err
		}
		defer release()
	}

	// Pre-parse just enough of the payload to know whether to prewarm;
	// keeps the goroutine decision fully local to this handler. If
	// decode fails, the actual Run will surface ErrInvalidPayload — we
	// just skip the prewarm on the failure path.
	shouldPrewarm := false
	if pp, perr := scriptpkg.DecodeGeneratePayload(j.Payload); perr == nil && pp != nil {
		spec := &pp.Spec
		shouldPrewarm = ShouldStart(spec.GenerateSceneImages, len(spec.ClipIDs), spec.NumClips)
	}
	if pu.prewarmUC != nil {
		_ = pu.prewarmUC.Start(ctx, j.ID, shouldPrewarm)
	}

	return pu.Run(ctx, j, tools)
}

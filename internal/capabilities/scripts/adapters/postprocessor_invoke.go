package adapters

import (
	"context"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// invokePostProcessor owns timeout, canonical timing measurement and result
// isolation. The composite runner can therefore focus on policy and merge
// decisions instead of transport/timing mechanics.
func (r *PostProcessorRegistry) invokePostProcessor(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, name ProcessorName, proc PostProcessor, result *PipelineResult) (kernobs.StageReport, *PostProcessResult, error) {
	var (
		stageReport kernobs.StageReport
		ppResult    *PostProcessResult
		err         error
	)
	if kernobs.FromContext(ctx) != nil {
		stageReport, err = kernobs.MeasureStageReport(ctx, kernobs.StageName(name), func(stageCtx context.Context) error {
			processorCtx, cancel := context.WithTimeout(stageCtx, postprocessorOperationTimeout)
			defer cancel()
			var processErr error
			ppResult, processErr = proc.Process(processorCtx, plan, input)
			return processErr
		})
	} else {
		start := time.Now()
		processorCtx, cancel := context.WithTimeout(ctx, postprocessorOperationTimeout)
		ppResult, err = proc.Process(processorCtx, plan, input)
		cancel()
		stageReport = kernobs.StageReport{
			Name: string(name), Status: kernobs.StageStatusCompleted,
			DurationMs: time.Since(start).Milliseconds(),
		}
		if err != nil {
			stageReport.Status = kernobs.StageStatusFailed
		}
	}
	adapter := r.canonicalTimingAdapter()
	if adapter == nil {
		adapter = &CanonicalTimingAdapter{VidRush: r.vidRushTimingMetrics()}
	}
	if projectionErr := adapter.ProjectStage(ctx, result, string(name), stageReport); projectionErr != nil && r.log != nil {
		r.log.Warn("canonical timing projection failed", zap.String("name", string(name)), zap.Error(projectionErr))
	}
	return stageReport, clonePostProcessResult(ppResult), err
}

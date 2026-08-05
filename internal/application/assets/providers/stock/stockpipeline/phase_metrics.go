package stockpipeline

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

const stockProcessType = "stock"

func startStockPhase(ctx context.Context, _ StepRunner, phase string) *kernobs.StageHandle {
	return kernobs.BeginStage(ctx, kernobs.StageName(phase))
}
func finishStockPhase(runner StepRunner, h *kernobs.StageHandle, phase string, err error) {
	if h == nil {
		return
	}
	report := h.End(err)
	if err != nil && runner != nil && runner.Log() != nil {
		runner.Log().Warn("stock: canonical process phase observation failed", zap.String("phase", phase), zap.String("status", report.Status), zap.Error(err))
	}
}
func startServiceStockPhase(ctx context.Context, phase, _ string) *kernobs.StageHandle {
	return kernobs.BeginStage(ctx, kernobs.StageName(phase))
}
func finishServiceStockPhase(log *zap.Logger, h *kernobs.StageHandle, err error) {
	if h == nil {
		return
	}
	report := h.End(err)
	if err != nil && log != nil {
		log.Warn("stock: canonical process phase observation failed", zap.String("status", report.Status), zap.Error(err))
	}
}
func prepareStockDriveArtifact(ctx context.Context, runner StepRunner, artifact finalization.VerifiedArtifact, _ map[string]any) (published finalization.PublishedArtifact, err error) {
	prepare := func(operationCtx context.Context) error {
		published, err = runner.ArtifactPreparation().Prepare(operationCtx, artifact)
		return err
	}
	if run := kernobs.FromContext(ctx); run != nil {
		err = run.Operation(ctx, kernobs.OperationInfo{
			Stage:     kernobs.StagePublish,
			Component: kernobs.ComponentDrive,
			Operation: kernobs.OperationUpload,
			Items:     1,
			Bytes:     artifact.SizeBytes,
		}, prepare)
		return published, err
	}
	return published, prepare(ctx)
}
func boolToInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func sumPlanDuration(plans []ClipPlan) float64 {
	var total float64
	for _, p := range plans {
		if p.EndSec > p.StartSec {
			total += p.EndSec - p.StartSec
		}
	}
	return total
}
func sumChunkDuration(chunks []ChunkState) float64 {
	var total float64
	for _, c := range chunks {
		if c.EndSec > c.StartSec {
			total += c.EndSec - c.StartSec
		}
	}
	return total
}

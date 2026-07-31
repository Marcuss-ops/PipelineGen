package stockpipeline

import (
	"context"

	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"go.uber.org/zap"
)

const stockProcessType = "stock"

type metricsRunner interface {
	MetricsRecorder() appmetrics.Recorder
}

func recorderForRunner(runner StepRunner) appmetrics.Recorder {
	if typed, ok := runner.(metricsRunner); ok {
		return typed.MetricsRecorder()
	}
	return nil
}

func startStockPhase(ctx context.Context, runner StepRunner, phase string) *appmetrics.Handle {
	return startStockPhaseWithRecorder(ctx, recorderForRunner(runner), phase, runner.JobID())
}

func startStockPhaseWithRecorder(ctx context.Context, recorder appmetrics.Recorder, phase, fallbackJobID string) *appmetrics.Handle {
	if recorder == nil {
		return nil
	}
	jobID, parentJobID := appmetrics.RunIDs(ctx)
	if jobID == "" {
		jobID = fallbackJobID
	}
	return recorder.Start(ctx, appmetrics.StartInput{
		ProcessType: stockProcessType,
		JobID:       jobID,
		ParentJobID: parentJobID,
		Phase:       phase,
		Provider:    "stock",
	})
}

func finishStockPhase(runner StepRunner, handle *appmetrics.Handle, phase string, phaseErr error) {
	if handle == nil {
		return
	}
	if err := handle.End(phaseErr); err != nil && runner != nil && runner.Log() != nil {
		runner.Log().Warn("stock: process phase metric persistence failed",
			zap.String("phase", phase),
			zap.Error(err))
	}
}

func startServiceStockPhase(ctx context.Context, recorder appmetrics.Recorder, phase, jobID string) *appmetrics.Handle {
	if recorder == nil {
		return nil
	}
	return startStockPhaseWithRecorder(ctx, recorder, phase, jobID)
}

func finishServiceStockPhase(log *zap.Logger, handle *appmetrics.Handle, phaseErr error) {
	if handle == nil {
		return
	}
	if err := handle.End(phaseErr); err != nil && log != nil {
		log.Warn("stock: process phase metric persistence failed", zap.Error(err))
	}
}

// prepareStockDriveArtifact records the canonical Drive publication boundary.
// All Stock ArtifactPreparation calls use this helper so video and metadata
// uploads share one metric shape without duplicating timing logic.
func prepareStockDriveArtifact(ctx context.Context, runner StepRunner, artifact finalization.VerifiedArtifact, details map[string]any) (published finalization.PublishedArtifact, err error) {
	metric := startStockPhase(ctx, runner, "stock.drive_upload")
	published, err = runner.ArtifactPreparation().Prepare(ctx, artifact)
	if metric != nil {
		out := int64(0)
		if err == nil {
			out = 1
		}
		metric.SetItems(1, out)
		metric.SetBytes(artifact.SizeBytes, artifact.SizeBytes)
		metric.SetDetails(details)
		finishStockPhase(runner, metric, "stock.drive_upload", err)
	}
	return published, err
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func sumPlanDuration(plans []ClipPlan) float64 {
	var total float64
	for _, plan := range plans {
		if plan.EndSec > plan.StartSec {
			total += plan.EndSec - plan.StartSec
		}
	}
	return total
}

func sumChunkDuration(chunks []ChunkState) float64 {
	var total float64
	for _, chunk := range chunks {
		if chunk.EndSec > chunk.StartSec {
			total += chunk.EndSec - chunk.StartSec
		}
	}
	return total
}

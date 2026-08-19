package observability

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

type Recorder interface {
	SaveReport(context.Context, *RunReport) error
}

type LifecycleRecorder interface {
	StartReport(context.Context, *RunReport) error
	AppendStage(context.Context, string, StageReport) error
	AppendOperation(context.Context, string, OperationReport) error
	RecordChild(context.Context, *RunReport) error
}

// OperationReportProjectionRecorder receives the canonical operation fact.
// All analytics/read-model sinks must implement this seam; boundary payloads
// are promoted to OperationReport before they reach storage.
type OperationReportProjectionRecorder interface {
	RecordOperationReport(context.Context, OperationReport) error
}

type AbandonedRunReconciler interface {
	RecoverAbandoned(context.Context, time.Time) (int64, error)
}

type RecorderFailureLogger interface {
	LogRecorderFailure(context.Context, string, string, error)
}

var recorderFailures atomic.Uint64

func RecorderFailureCount() uint64 { return recorderFailures.Load() }

func noteRecorderFailure(ctx context.Context, runID, operation string, err error, logger RecorderFailureLogger) {
	if err == nil {
		return
	}
	recorderFailures.Add(1)
	if logger != nil {
		logger.LogRecorderFailure(ctx, runID, operation, err)
		return
	}
	slog.Default().ErrorContext(ctx, "observability recorder failure", "run_id", runID, "operation", operation, "error", err)
}

type NoopRecorder struct{}

func (NoopRecorder) SaveReport(context.Context, *RunReport) error { return nil }

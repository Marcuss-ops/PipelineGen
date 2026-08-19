package observability

import (
	"context"
	"time"
)

// MeasuredOperation is the boundary input used by owner-measured adapters.
// Deprecated: convert it immediately to OperationReport with
// OperationReportFromMeasuredOperation. OperationReport is the only canonical
// operation fact and is what recorders and projections must consume.
type MeasuredOperation struct {
	// ObservationID is shared by the canonical run observation and every
	// analytics projection. Empty IDs are assigned by the boundary before the
	// fact is sent to more than one sink.
	ObservationID                     string
	Stage, Component, Provider        string
	Operation                         string
	SourceSHA256                      string
	SourceDurationMS                  int64
	SourceSizeBytes                   int64
	Width, Height                     int
	FPS                               float64
	InputCodec, OutputCodec           string
	ElapsedMS                         int64
	QueueWaitMS                       int64
	QueuedAt, StartedAt, FinishedAt   time.Time
	WorkerID                          string
	CPUUserMS, CPUSystemMS            int64
	OutputSizeBytes                   int64
	OutputDurationMS                  int64
	CacheHit                          bool
	Strategy, MetadataJSON, CreatedAt string
}

// OperationReportFromMeasuredOperation promotes the one boundary measurement
// into the canonical operation contract. The same observation ID is retained
// so all projections refer to exactly one fact.
func OperationReportFromMeasuredOperation(m MeasuredOperation) OperationReport {
	if m.ObservationID == "" {
		m.ObservationID = NewObservationID()
	}
	return OperationReport{
		ObservationID: m.ObservationID, Stage: m.Stage, Component: m.Component, Provider: m.Provider, Operation: m.Operation,
		Status: StageStatusCompleted, Attempts: 1, DurationMs: nonNegative(m.ElapsedMS),
		QueueWaitMs: nonNegative(m.QueueWaitMS), QueuedAt: m.QueuedAt, StartedAt: m.StartedAt,
		FinishedAt: m.FinishedAt, WorkerID: m.WorkerID,
		SourceSHA256: m.SourceSHA256, SourceDurationMS: nonNegative(m.SourceDurationMS),
		SourceSizeBytes: nonNegative(m.SourceSizeBytes), OutputSizeBytes: nonNegative(m.OutputSizeBytes), OutputDurationMS: nonNegative(m.OutputDurationMS),
		CPUUserMS: nonNegative(m.CPUUserMS), CPUSystemMS: nonNegative(m.CPUSystemMS),
		Width: m.Width, Height: m.Height, FPS: m.FPS, InputCodec: m.InputCodec,
		OutputCodec: m.OutputCodec, CacheHit: m.CacheHit, Strategy: m.Strategy,
		MetadataJSON: m.MetadataJSON, CreatedAt: m.CreatedAt,
	}
}

func (r *Run) RecordMeasuredOperation(m MeasuredOperation) {
	if r == nil || m.Operation == "" {
		return
	}
	r.recordOperation(OperationReportFromMeasuredOperation(m))
}

func RecordMeasuredOperation(ctx context.Context, m MeasuredOperation) {
	if run := FromContext(ctx); run != nil {
		run.RecordMeasuredOperation(m)
	}
}

package observability

import (
	"context"
	"time"
)

// MeasuredOperation is the canonical owner-measured operation payload.
// Capability and storage packages may project it, but must not redefine it.
type MeasuredOperation struct {
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

func (r *Run) RecordMeasuredOperation(m MeasuredOperation) {
	if r == nil || m.Operation == "" {
		return
	}
	r.recordOperation(OperationReport{
		ObservationID: NewObservationID(), Stage: m.Stage, Component: m.Component, Provider: m.Provider, Operation: m.Operation,
		Status: StageStatusCompleted, Attempts: 1, DurationMs: nonNegative(m.ElapsedMS),
		QueueWaitMs: nonNegative(m.QueueWaitMS), QueuedAt: m.QueuedAt, StartedAt: m.StartedAt,
		FinishedAt: m.FinishedAt, WorkerID: m.WorkerID,
		SourceSHA256: m.SourceSHA256, SourceDurationMS: nonNegative(m.SourceDurationMS),
		SourceSizeBytes: nonNegative(m.SourceSizeBytes), OutputSizeBytes: nonNegative(m.OutputSizeBytes), OutputDurationMS: nonNegative(m.OutputDurationMS),
		CPUUserMS: nonNegative(m.CPUUserMS), CPUSystemMS: nonNegative(m.CPUSystemMS),
		Width: m.Width, Height: m.Height, FPS: m.FPS, InputCodec: m.InputCodec,
		OutputCodec: m.OutputCodec, CacheHit: m.CacheHit, Strategy: m.Strategy,
		MetadataJSON: m.MetadataJSON, CreatedAt: m.CreatedAt,
	})
}

func RecordMeasuredOperation(ctx context.Context, m MeasuredOperation) {
	if run := FromContext(ctx); run != nil {
		run.RecordMeasuredOperation(m)
	}
}

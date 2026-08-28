package observability

import (
	"context"
	"time"
)

// OperationInfo describes one external-boundary operation.
type OperationInfo struct {
	Stage     StageName
	Component ComponentName
	Operation OperationName
	Provider  string
	// CacheStatus records the outcome of the cache lookup around the call.
	CacheStatus  string
	Items        int64
	Bytes        int64
	WorkerID     string
	QueuedAt     time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
	MetadataJSON string
	// OnRecord, when non-nil, is invoked with the finalized OperationReport
	// immediately before it is recorded. Owners use it to attach facts that
	// are only known AFTER the call completes (e.g. tokens, model-load,
	// and inference durations reported by an external service) without
	// re-timing the boundary. It runs on the success/error path only, never
	// on the panic path.
	OnRecord func(*OperationReport)
}

// Operation measures one external-boundary call:
//
//	err := run.Operation(ctx, observability.OperationInfo{
//	    Stage:     observability.StageAcquire,
//	    Component: observability.ComponentYouTube,
//	    Operation: observability.OperationDownload,
//	}, func(ctx context.Context) error {
//	    return youtubeDownloader.Download(ctx, url)
//	})
//
// The timer is always closed: success → completed, error → failed with the
// error code, panic → failed and re-raised.
func (r *Run) Operation(ctx context.Context, info OperationInfo, fn func(context.Context) error) error {
	if r == nil || info.Operation == "" {
		if fn == nil {
			return nil
		}
		return fn(ctx)
	}
	started := r.now()
	queued := info.QueuedAt
	if queued.IsZero() {
		queued = started
	}
	op := OperationReport{
		ObservationID: NewObservationID(),
		Stage:         string(info.Stage),
		Component:     string(info.Component),
		Operation:     string(info.Operation),
		Provider:      info.Provider,
		Status:        StageStatusRunning,
		Attempts:      1,
		Items:         nonNegative(info.Items),
		Bytes:         nonNegative(info.Bytes),
		CacheStatus:   info.CacheStatus,
		WorkerID:      info.WorkerID,
		QueuedAt:      queued,
		StartedAt:     started,
		MetadataJSON:  info.MetadataJSON,
	}
	if fn == nil {
		op.Status = StageStatusCompleted
		r.recordOperation(op)
		return nil
	}
	err := func() (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				op.Status = StageStatusFailed
				op.ErrorCode = errorCode(panicError(rec))
				op.FinishedAt = r.now()
				op.DurationMs = nonNegative(op.FinishedAt.Sub(started).Milliseconds())
				op.QueueWaitMs = nonNegative(started.Sub(queued).Milliseconds())
				r.recordOperation(op)
				panic(rec)
			}
		}()
		return fn(ctx)
	}()
	op.FinishedAt = r.now()
	if !info.FinishedAt.IsZero() {
		op.FinishedAt = info.FinishedAt
	}
	op.DurationMs = nonNegative(op.FinishedAt.Sub(started).Milliseconds())
	op.QueueWaitMs = nonNegative(started.Sub(queued).Milliseconds())
	if err != nil {
		op.Status = StageStatusFailed
		op.ErrorCode = errorCode(err)
	} else {
		op.Status = StageStatusCompleted
	}
	if info.OnRecord != nil {
		info.OnRecord(&op)
	}
	r.recordOperation(op)
	return err
}

// MeasureOperation records an operation on the run bound to ctx. When no run
// is bound it degrades to a plain pass-through call (instrumentation must
// never change behaviour).
func MeasureOperation(ctx context.Context, info OperationInfo, fn func(context.Context) error) error {
	run := FromContext(ctx)
	if run == nil {
		if fn == nil {
			return nil
		}
		return fn(ctx)
	}
	return run.Operation(ctx, info, fn)
}

// RecordOperation appends an operation observation that was measured by the
// operation's OWNER (a subprocess, a remote service, or an external tool that
// reports its own timings). The caller supplies the measured duration; the
// kernel never re-times the operation, so an owner-owned boundary is never
// double-measured. A non-positive duration is recorded as 0.
func (r *Run) RecordOperation(info OperationInfo, durationMs int64) {
	if r == nil || info.Operation == "" {
		return
	}
	op := OperationReport{
		ObservationID: NewObservationID(),
		Stage:         string(info.Stage),
		Component:     string(info.Component),
		Operation:     string(info.Operation),
		Provider:      info.Provider,
		Status:        StageStatusCompleted,
		Attempts:      1,
		DurationMs:    nonNegative(durationMs),
		Items:         nonNegative(info.Items),
		Bytes:         nonNegative(info.Bytes),
		CacheStatus:   info.CacheStatus,
		WorkerID:      info.WorkerID,
		QueuedAt:      info.QueuedAt,
		StartedAt:     info.StartedAt,
		FinishedAt:    info.FinishedAt,
		QueueWaitMs:   nonNegative(info.StartedAt.Sub(info.QueuedAt).Milliseconds()),
		MetadataJSON:  info.MetadataJSON,
	}
	r.recordOperation(op)
}

// RecordOperation is the context-bound form of Run.RecordOperation: it appends
// the owner-measured operation to the run bound to ctx, or is a no-op when no
// run is bound (instrumentation must never change behaviour).
func RecordOperation(ctx context.Context, info OperationInfo, durationMs int64) {
	if run := FromContext(ctx); run != nil {
		run.RecordOperation(info, durationMs)
	}
}

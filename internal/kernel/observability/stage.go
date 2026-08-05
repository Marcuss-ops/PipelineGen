package observability

import "context"

// StageInfo describes one canonical pipeline stage execution.
type StageInfo struct {
	Stage          StageName
	CacheStatus    string
	ItemsInput     int64
	ItemsCompleted int64
	ItemsFailed    int64
	BytesProcessed int64
}

// Stage measures one canonical pipeline stage:
//
//	err := run.Stage(ctx, observability.StageAcquire, func(ctx context.Context) error {
//	    return downloader.Download(ctx, request)
//	})
//
// The timer is always closed: on success it records completed, on error it
// records failed with the error code, and on panic it records failed and
// re-raises the panic.
func (r *Run) Stage(ctx context.Context, stage StageName, fn func(context.Context) error) error {
	return r.StageWith(ctx, StageInfo{Stage: stage}, fn)
}

// StageWith measures one stage with explicit item/byte counters and cache
// status. Unlabelled stages (empty Stage) are not measured.
func (r *Run) StageWith(ctx context.Context, info StageInfo, fn func(context.Context) error) error {
	if r == nil || info.Stage == "" {
		if fn == nil {
			return nil
		}
		return fn(ctx)
	}
	start := r.now()
	st := StageReport{
		Name:           string(info.Stage),
		Status:         StageStatusRunning,
		Attempts:       1,
		CacheStatus:    info.CacheStatus,
		ItemsInput:     nonNegative(info.ItemsInput),
		ItemsCompleted: nonNegative(info.ItemsCompleted),
		ItemsFailed:    nonNegative(info.ItemsFailed),
		BytesProcessed: nonNegative(info.BytesProcessed),
	}
	if fn == nil {
		st.Status = StageStatusCompleted
		r.recordStage(st)
		return nil
	}
	err := func() (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				st.Status = StageStatusFailed
				st.ErrorCode = errorCode(panicError(rec))
				st.DurationMs = nonNegative(r.now().Sub(start).Milliseconds())
				r.recordStage(st)
				panic(rec)
			}
		}()
		return fn(ctx)
	}()
	st.DurationMs = nonNegative(r.now().Sub(start).Milliseconds())
	if err != nil {
		st.Status = StageStatusFailed
		st.ErrorCode = errorCode(err)
	} else {
		st.Status = StageStatusCompleted
	}
	r.recordStage(st)
	return err
}

// MeasureStage records a stage on the run bound to ctx. When no run is bound
// it degrades to a plain pass-through call (instrumentation must never change
// behaviour).
func MeasureStage(ctx context.Context, stage StageName, fn func(context.Context) error) error {
	run := FromContext(ctx)
	if run == nil {
		if fn == nil {
			return nil
		}
		return fn(ctx)
	}
	return run.Stage(ctx, stage, fn)
}

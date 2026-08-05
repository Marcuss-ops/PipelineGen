package observability

import (
	"context"
	"time"
)

// StageHandle is the context-bound timer form of one canonical pipeline
// stage. BeginStage starts it, counter setters enrich it, and End closes and
// records it on the run bound to the originating context. It complements the
// functional Stage/StageWith API (stage.go) for call sites that must
// interleave external work with explicit counter updates.
//
// When no run is bound to ctx (plain context, or a code path the runtime has
// not yet wired), BeginStage returns nil so instrumentation degrades to a
// no-op and never changes behaviour (godlike/07 no-fake-availability).
type StageHandle struct {
	run    *Run
	st     StageReport
	now    func() time.Time
	closed bool
}

// BeginStage starts one canonical stage on the run bound to ctx.
//
//	handle := BeginStage(ctx, StageProcess)
//	handle.SetItems(1, 1)
//	report := handle.End(err)
//
// The stage is not recorded until End is called, so a handle that is never
// closed leaks nothing observable.
func BeginStage(ctx context.Context, name StageName) *StageHandle {
	run := FromContext(ctx)
	if run == nil || name == "" {
		return nil
	}
	now := run.now
	if now == nil {
		now = time.Now
	}
	return &StageHandle{
		run: run,
		st: StageReport{
			ObservationID: NewObservationID(),
			Name:          string(name),
			Status:        StageStatusRunning,
			StartedAt:     now(),
		},
		now: now,
	}
}

// SetItems records the requested and completed item counts.
func (h *StageHandle) SetItems(in, out int64) {
	if h == nil {
		return
	}
	h.st.ItemsInput = nonNegative(in)
	h.st.ItemsCompleted = nonNegative(out)
}

// SetItemsFailed records the failed item count.
func (h *StageHandle) SetItemsFailed(n int64) {
	if h == nil {
		return
	}
	h.st.ItemsFailed = nonNegative(n)
}

// SetBytes records the processed byte count. The canonical stage report owns
// a single BytesProcessed counter, so the legacy input/output byte pair is
// projected onto it (out wins) and the schema stays stable.
func (h *StageHandle) SetBytes(in, out int64) {
	if h == nil {
		return
	}
	h.st.BytesProcessed = nonNegative(out)
}

// SetAttempts records the number of attempts represented by the stage.
func (h *StageHandle) SetAttempts(n int) {
	if h == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	h.st.Attempts = n
}

// SetCacheStatus records the typed cache outcome for the stage.
func (h *StageHandle) SetCacheStatus(status string) {
	if h == nil {
		return
	}
	h.st.CacheStatus = status
}

// SetErrorCode records a typed error code for callers that finish a stage
// with a domain-specific failure classification.
func (h *StageHandle) SetErrorCode(code string) {
	if h == nil {
		return
	}
	h.st.ErrorCode = code
}

// End closes the stage, records it on the bound run, and returns the final
// report. Safe to call once; repeated calls return the recorded report.
func (h *StageHandle) End(err error) StageReport {
	if h == nil {
		return StageReport{}
	}
	if h.closed {
		return h.st
	}
	h.closed = true
	h.st.FinishedAt = h.now()
	h.st.DurationMs = nonNegative(h.st.FinishedAt.Sub(h.st.StartedAt).Milliseconds())
	if err != nil {
		h.st.Status = StageStatusFailed
		h.st.ErrorCode = errorCode(err)
	} else {
		h.st.Status = StageStatusCompleted
	}
	h.run.recordStage(h.st)
	return h.st
}

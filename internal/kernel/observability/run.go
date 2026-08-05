package observability

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// RunInfo identifies and parameterises one job execution.
type RunInfo struct {
	// RunID is the canonical run identifier. When empty, StartRun generates
	// one via NewRunID.
	RunID string
	// JobID is the canonical job identifier (jobs table row).
	JobID string
	// JobType is the canonical job type (script.generate, voiceover.generate,
	// stock.run, youtube.clip.extract, ...).
	JobType string
	// AttemptID is the canonical job attempt owned by the runtime. Empty for
	// first attempts on runtimes that have not materialised attempts yet.
	AttemptID string
	// ParentRunID links this run to its parent run (child jobs).
	ParentRunID string
	// CreatedAt is the job enqueue timestamp. Zero falls back to start time.
	CreatedAt time.Time
	// QueueWaitMs is the time the job spent waiting before processing started.
	// Computed by the runtime at claim time (now - CreatedAt).
	QueueWaitMs int64
}

// RunObserver is the composition entry point: it creates Runs and owns the
// durable sink. One observer is shared process-wide.
type RunObserver struct {
	recorder  Recorder
	collector Collector
	now       func() time.Time
}

// NewRunObserver constructs a RunObserver. A nil recorder makes the observer a
// safe in-memory-only observer: reports are still produced and serialisable,
// they are just not persisted.
func NewRunObserver(recorder Recorder) *RunObserver {
	return &RunObserver{recorder: recorder, now: time.Now}
}

// WithCollector attaches a best-effort metrics/report collector to the
// observer. It is intended for composition-root wiring before StartRun is
// called; the returned observer is the same pointer for fluent setup.
func (o *RunObserver) WithCollector(collector Collector) *RunObserver {
	if o != nil {
		o.collector = collector
	}
	return o
}

// NewRunObserverWithCollector constructs an observer with both the durable
// recorder and the metrics/report collector configured.
func NewRunObserverWithCollector(recorder Recorder, collector Collector) *RunObserver {
	return NewRunObserver(recorder).WithCollector(collector)
}

// StartRun begins a new Run with the canonical report envelope. The returned
// run must be finished exactly once (defer run.Finish() or the body-runner
// Run method); Finish is idempotent.
func (o *RunObserver) StartRun(ctx context.Context, info RunInfo) *Run {
	now := time.Now
	var rec Recorder
	var collector Collector
	if o != nil {
		if o.now != nil {
			now = o.now
		}
		rec = o.recorder
		collector = o.collector
	}
	if ctx == nil {
		ctx = context.Background()
	}
	createdAt := info.CreatedAt
	if createdAt.IsZero() {
		createdAt = now()
	}
	startedAt := now()
	runID := info.RunID
	if runID == "" {
		runID = NewRunID()
	}
	return &Run{
		ctx:       ctx,
		rec:       rec,
		collector: collector,
		now:       now,
		started:   startedAt,
		report: RunReport{
			RunID:       runID,
			JobID:       info.JobID,
			JobType:     info.JobType,
			AttemptID:   info.AttemptID,
			ParentRunID: info.ParentRunID,
			Status:      StatusRunning,
			CreatedAt:   createdAt,
			StartedAt:   startedAt,
			QueueWaitMs: nonNegative(info.QueueWaitMs),
		},
	}
}

// Run runs body under a fresh run bound to the context. It is the panic-safe
// entry point: on panic the run is closed as FAILED (timer closed) and the
// panic is re-raised. Equivalent to:
//
//	run := observer.StartRun(ctx, info)
//	defer run.Finish()
//	ctx = observability.WithRun(ctx, run)
//
// but guarantees the run is closed even when body panics or returns an error.
func (o *RunObserver) Run(ctx context.Context, info RunInfo, body func(ctx context.Context) error) (*RunReport, error) {
	run := o.StartRun(ctx, info)
	runCtx := WithRun(ctx, run)
	defer func() {
		if rec := recover(); rec != nil {
			run.FinishWithPanic(rec)
			panic(rec)
		}
	}()
	if body == nil {
		err := errorCodeMissingBody
		run.FinishWithError(err)
		return run.Report(), err
	}
	err := body(runCtx)
	run.FinishWithError(err)
	return run.Report(), err
}

// Run is the per-job accumulation buffer. It is safe for concurrent use:
// stages, operations, counters and artifacts may be recorded from parallel
// goroutines (child jobs, provider fan-out).
type Run struct {
	ctx       context.Context
	rec       Recorder
	collector Collector
	now       func() time.Time
	started   time.Time

	mu       sync.Mutex
	report   RunReport
	finished bool

	// activeMs is the sum of stage durations; operationMs is the sum of
	// operation durations (the parallelism diagnostic).
	activeMs    int64
	operationMs int64
}

// Finish closes the run as SUCCEEDED (first call wins). Idempotent: later
// calls return the same report. The report is flushed to the recorder sink
// exactly once. After Finish the report is sealed: further stage/operation/
// counter/child/artifact mutations are ignored (the finished report is the
// canonical snapshot).
func (r *Run) Finish() *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out, emit := r.finishLocked(nil, "")
	r.mu.Unlock()
	if emit {
		r.emit(out)
	}
	return out
}

// FinishWithError closes the run as FAILED with the error code/message.
func (r *Run) FinishWithError(err error) *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out, emit := r.finishLocked(err, "")
	r.mu.Unlock()
	if emit {
		r.emit(out)
	}
	return out
}

// FinishWithPanic closes the run as FAILED from a recovered panic value. Used
// by the panic-safe body runner; the panic is re-raised by the caller.
func (r *Run) FinishWithPanic(v any) *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out, emit := r.finishLocked(panicError(v), "")
	r.mu.Unlock()
	if emit {
		r.emit(out)
	}
	return out
}

// Cancel closes the run as CANCELLED.
func (r *Run) Cancel() *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out, emit := r.finishLocked(nil, StatusCancelled)
	r.mu.Unlock()
	if emit {
		r.emit(out)
	}
	return out
}

// finishLocked must be called with r.mu held. First call wins. The bool
// reports whether the caller must emit the newly finalized report.
func (r *Run) finishLocked(finalErr error, forcedStatus string) (*RunReport, bool) {
	if r.finished {
		return r.reportCopyLocked(), false
	}
	r.finished = true
	now := r.now()
	if r.report.FinishedAt.IsZero() {
		r.report.FinishedAt = now
	}
	r.report.WallTimeMs = nonNegative(r.report.FinishedAt.Sub(r.started).Milliseconds())
	r.report.ActiveMs = r.activeMs
	r.report.AccumulatedOperationMs = r.operationMs
	switch {
	case forcedStatus != "":
		r.report.Status = forcedStatus
	case finalErr != nil:
		r.report.Status = StatusFailed
		r.report.Error = finalErr.Error()
		r.report.ErrorCode = errorCode(finalErr)
	default:
		r.report.Status = StatusSucceeded
	}
	return r.reportCopyLocked(), true
}

// emit sends independent defensive copies to each best-effort sink. Sink
// implementations may retain or mutate their input without affecting the
// caller or another sink.
func (r *Run) emit(report *RunReport) {
	if report == nil {
		return
	}
	if r.collector != nil {
		_ = r.collector.Collect(r.ctx, cloneReport(report))
	}
	if r.rec != nil {
		_ = r.rec.SaveReport(r.ctx, cloneReport(report))
	}
}

// Report returns a defensive copy of the current report. When the run is not
// yet finished, Status is RUNNING and FinishedAt is zero.
func (r *Run) Report() *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reportCopyLocked()
}

func (r *Run) reportCopyLocked() *RunReport {
	return cloneReport(&r.report)
}

func cloneReport(report *RunReport) *RunReport {
	if report == nil {
		return nil
	}
	out := *report
	out.Stages = append([]StageReport(nil), report.Stages...)
	out.Operations = append([]OperationReport(nil), report.Operations...)
	out.Artifacts = append([]ArtifactReport(nil), report.Artifacts...)
	if report.Children != nil {
		child := *report.Children
		out.Children = &child
	}
	return &out
}

// JSON serialises the current report. A nil run yields (nil, nil).
func (r *Run) JSON() ([]byte, error) {
	rep := r.Report()
	if rep == nil {
		return nil, nil
	}
	return json.Marshal(rep)
}

// AddBlocked records time spent waiting (semaphore, rate-limit, retry
// backoff). Negative values are clamped to zero. Mutations after Finish are
// ignored: the finished report is the canonical sealed snapshot.
func (r *Run) AddBlocked(d time.Duration) {
	if r == nil || d <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.BlockedMs += d.Milliseconds()
}

// RegisterChild links a finished child run into the parent's children summary.
// Requested/completed/failed counters and the summed child wall time are
// updated; durations are never averaged, so parallel children sum correctly.
func (r *Run) RegisterChild(report *RunReport) {
	if r == nil || report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	if r.report.Children == nil {
		r.report.Children = &ChildrenSummary{}
	}
	r.report.Children.Requested++
	switch report.Status {
	case StatusSucceeded:
		r.report.Children.Completed++
	case StatusFailed, StatusCancelled:
		r.report.Children.Failed++
	}
	r.report.Children.WallTimeMs += report.WallTimeMs
}

// AddArtifact records one produced/reused artifact and updates the counters.
func (r *Run) AddArtifact(a ArtifactReport) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.Artifacts = append(r.report.Artifacts, a)
	if a.Reused {
		r.report.Counters.ArtifactsReused++
	} else {
		r.report.Counters.ArtifactsCreated++
	}
}

// Counter helpers. All are nil-safe and clamp negative increments to zero.

func (r *Run) AddItemsRequested(n int64) {
	r.addCounter(func(c *RunCounters) { c.ItemsRequested += nonNegative(n) })
}

func (r *Run) AddItemsCompleted(n int64) {
	r.addCounter(func(c *RunCounters) { c.ItemsCompleted += nonNegative(n) })
}

func (r *Run) AddItemsFailed(n int64) {
	r.addCounter(func(c *RunCounters) { c.ItemsFailed += nonNegative(n) })
}

func (r *Run) IncCacheHit()  { r.addCounter(func(c *RunCounters) { c.CacheHits++ }) }
func (r *Run) IncCacheMiss() { r.addCounter(func(c *RunCounters) { c.CacheMisses++ }) }
func (r *Run) IncRetry()     { r.addCounter(func(c *RunCounters) { c.Retries++ }) }

// SetRetries records the number of retries already consumed before this
// attempt (the runtime's canonical job.RetryCount at claim time). Unlike the
// additive counters it overwrites: the value is a snapshot from the job row,
// not an in-run accumulation.
func (r *Run) SetRetries(n int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.Counters.Retries = nonNegative(n)
}

func (r *Run) AddBytesDownloaded(n int64) {
	r.addCounter(func(c *RunCounters) { c.BytesDownloaded += nonNegative(n) })
}

func (r *Run) AddBytesUploaded(n int64) {
	r.addCounter(func(c *RunCounters) { c.BytesUploaded += nonNegative(n) })
}

func (r *Run) IncArtifactCreated() { r.addCounter(func(c *RunCounters) { c.ArtifactsCreated++ }) }
func (r *Run) IncArtifactReused()  { r.addCounter(func(c *RunCounters) { c.ArtifactsReused++ }) }

func (r *Run) addCounter(fn func(*RunCounters)) {
	if r == nil || fn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	fn(&r.report.Counters)
}

// recordStage appends a closed stage report and accumulates active time.
// Must be safe under concurrent Stage calls.
func (r *Run) recordStage(st StageReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.Stages = append(r.report.Stages, st)
	r.activeMs += st.DurationMs
}

// recordOperation appends a closed operation report and accumulates the
// operation time (parallelism diagnostic).
func (r *Run) recordOperation(op OperationReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.Operations = append(r.report.Operations, op)
	r.operationMs += op.DurationMs
}

// NewRunID returns a collision-resistant run identifier. Empty RunIDs in
// RunInfo are replaced with this at StartRun time.
func NewRunID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("run_%d_%x", time.Now().UnixNano(), b)
	}
	return fmt.Sprintf("run_%d", time.Now().UnixNano())
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

package observability

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/background"
)

// runReportSaveTimeout bounds the detached persistence flush in Run.emit.
// SaveReport / Collect run against a context detached from the run's
// lifecycle (see emit) so a worker shutdown that cancels the parent cannot
// strand a run in RUNNING forever, but the detached context is still
// bounded so a hung observability database cannot stall shutdown
// indefinitely.
const runReportSaveTimeout = 30 * time.Second

type RunInfo struct {
	RunID       string
	JobID       string
	JobType     string
	AttemptID   string
	LeaseID     string
	ParentRunID string
	ParentJobID string

	WorkerID       string
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
	QueueWaitMs    int64
}

type RunObserver struct {
	recorder  Recorder
	collector Collector
	now       func() time.Time
}

func NewRunObserver(recorder Recorder) *RunObserver {
	return &RunObserver{recorder: recorder, now: time.Now}
}
func (o *RunObserver) WithCollector(c Collector) *RunObserver {
	if o != nil {
		o.collector = c
	}
	return o
}
func NewRunObserverWithCollector(r Recorder, c Collector) *RunObserver {
	return NewRunObserver(r).WithCollector(c)
}

type ClaimRunInfo struct {
	JobID          string
	JobType        string
	AttemptID      string
	LeaseID        string
	WorkerID       string
	LeaseExpiresAt time.Time
	ParentRunID    string
	ParentJobID    string

	CreatedAt  time.Time
	StartedAt  *time.Time
	RetryCount int
}

func (o *RunObserver) StartRunForClaim(ctx context.Context, info ClaimRunInfo) *Run {
	queueWait := int64(0)
	if info.StartedAt != nil && info.StartedAt.After(info.CreatedAt) {
		queueWait = info.StartedAt.Sub(info.CreatedAt).Milliseconds()
	}
	if info.AttemptID == "" {
		panic("observability: AttemptID is required")
	}
	r := o.StartRun(ctx, RunInfo{JobID: info.JobID, JobType: info.JobType, AttemptID: info.AttemptID, LeaseID: info.LeaseID, WorkerID: info.WorkerID, LeaseExpiresAt: info.LeaseExpiresAt, ParentRunID: info.ParentRunID, ParentJobID: info.ParentJobID, CreatedAt: info.CreatedAt, QueueWaitMs: queueWait})
	r.SetRetries(int64(info.RetryCount))
	return r
}

func (o *RunObserver) StartRun(ctx context.Context, info RunInfo) *Run {
	if info.AttemptID == "" {
		panic("observability: AttemptID is required")
	}
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
	created := info.CreatedAt
	if created.IsZero() {
		created = now()
	}
	started := now()
	id := info.RunID
	if id == "" {
		id = NewRunID()
	}
	run := &Run{ctx: ctx, rec: rec, collector: collector, now: now, started: started, children: make(map[string]RunReport), report: RunReport{RunID: id, JobID: info.JobID, JobType: info.JobType, AttemptID: info.AttemptID, LeaseID: info.LeaseID, ParentRunID: info.ParentRunID, ParentJobID: info.ParentJobID, WorkerID: info.WorkerID, LeaseExpiresAt: info.LeaseExpiresAt, Status: StatusRunning, CreatedAt: created, StartedAt: started, QueueWaitMs: nonNegative(info.QueueWaitMs)}}
	run.startReport()
	return run
}

func (o *RunObserver) Run(ctx context.Context, info RunInfo, body func(context.Context) error) (*RunReport, error) {
	r := o.StartRun(ctx, info)
	runCtx := WithRun(ctx, r)
	defer func() {
		if v := recover(); v != nil {
			r.FinishWithPanic(v)
			panic(v)
		}
	}()
	if body == nil {
		err := errorCodeMissingBody
		r.FinishWithError(err)
		return r.Report(), err
	}
	err := body(runCtx)
	r.FinishWithError(err)
	return r.Report(), err
}

type Run struct {
	ctx         context.Context
	rec         Recorder
	collector   Collector
	now         func() time.Time
	started     time.Time
	mu          sync.Mutex
	report      RunReport
	finished    bool
	operationMs int64
	children    map[string]RunReport
}

func (r *Run) startReport() {
	if r == nil || r.rec == nil {
		return
	}
	l, ok := r.rec.(LifecycleRecorder)
	if !ok {
		return
	}
	if err := l.StartReport(r.ctx, r.Report()); err != nil {
		r.markRecorderFailure("start_report", err)
	}
}
func (r *Run) markRecorderFailure(op string, err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	r.report.ObservabilityDegraded = true
	id := r.report.RunID
	r.mu.Unlock()
	logger, _ := r.rec.(RecorderFailureLogger)
	noteRecorderFailure(r.ctx, id, op, err, logger)
}

func (r *Run) finish(status string, finalErr error) *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.finished {
		out := cloneReport(&r.report)
		r.mu.Unlock()
		return out
	}
	r.finished = true
	if r.report.FinishedAt.IsZero() {
		r.report.FinishedAt = r.now()
	}
	// Both values are derived from the runtime lifecycle events: execution
	// starts at the claimed/started event and ends at finished_at. Queue wait
	// is never part of execution wall and neither value belongs in a renderer
	// metrics contract.
	r.report.WallTimeMs = nonNegative(r.report.FinishedAt.Sub(r.started).Milliseconds())
	r.report.ExecutionWallMs = r.report.WallTimeMs
	r.report.AccumulatedOperationMs = r.operationMs

	// Derive the canonical timing breakdown from the top-level stage wall
	// times recorded on the SAME clock (never a second timer).
	bd := r.report.Breakdown()
	r.report.AttributedStageMs = bd.AttributedStageMs
	r.report.UnattributedMs = bd.UnattributedMs
	r.report.UnattributedPercent = bd.UnattributedPercent
	r.report.BottleneckStage = bd.BottleneckStage
	r.report.BottleneckOperation = bd.BottleneckOperation
	if status != "" {
		r.report.Status = status
	} else if finalErr != nil {
		r.report.Status = StatusFailed
		r.report.Error = finalErr.Error()
		r.report.ErrorCode = errorCode(finalErr)
	} else {
		r.report.Status = StatusSucceeded
	}
	out := cloneReport(&r.report)
	r.mu.Unlock()
	r.emit(out)
	return out
}
func (r *Run) Finish() *RunReport                   { return r.finish("", nil) }
func (r *Run) FinishWithError(err error) *RunReport { return r.finish("", err) }
func (r *Run) FinishWithPanic(v any) *RunReport     { return r.finish("", panicError(v)) }
func (r *Run) Cancel() *RunReport                   { return r.finish(StatusCancelled, nil) }

func (r *Run) emit(report *RunReport) {
	if r == nil || report == nil {
		return
	}
	// Detach the final persistence from the run's lifecycle context. The
	// run is bound to the worker's parent context (see
	// worker_execution.go::runJob), so when the worker shuts down the parent
	// is cancelled and SaveReport(r.ctx, ...) would fail with "context
	// canceled" — the terminal UPDATE run_observability never lands and the
	// run stays RUNNING forever (orphaned). background.DetachWithTimeout
	// preserves correlation values while removing parent cancellation, and
	// bounds the flush so a hung DB cannot stall shutdown.
	ctx, cancel := background.DetachWithTimeout(r.ctx, "run-report-save", runReportSaveTimeout)
	defer cancel()
	if r.collector != nil {
		_ = r.collector.Collect(ctx, cloneReport(report))
	}
	if r.rec != nil {
		if err := r.rec.SaveReport(ctx, cloneReport(report)); err != nil {
			r.markRecorderFailure("save_report", err)
		}
	}
}
func (r *Run) Report() *RunReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneReport(&r.report)
}

// ElapsedMs returns the wall time elapsed since the run started, measured on
// the run's own canonical clock (never a second timer). It is the canonical
// source for compatibility projections (e.g. GenerationTimings.TotalMs) that
// must not start an ad-hoc clock. started and now are write-once after
// StartRun, so they are read without the report mutex.
func (r *Run) ElapsedMs() int64 {
	if r == nil {
		return 0
	}
	return nonNegative(r.now().Sub(r.started).Milliseconds())
}

// TimingSummary returns the canonical diagnostic projection of the current
// run. It derives the breakdown from the run's live elapsed wall time
// (ElapsedMs), so callers can log the critical path and bottleneck before the
// run is finished. The finished-run form (RunReport.TimingSummary) uses the
// finalized WallTimeMs instead.
func (r *Run) TimingSummary() TimingSummary {
	if r == nil {
		return TimingSummary{}
	}
	return r.Report().timingSummaryWithWall(r.ElapsedMs())
}
func cloneReport(in *RunReport) *RunReport {
	if in == nil {
		return nil
	}
	out := *in
	out.Stages = append([]StageReport(nil), in.Stages...)
	out.Operations = append([]OperationReport(nil), in.Operations...)
	out.Artifacts = append([]ArtifactReport(nil), in.Artifacts...)
	out.Waits = append([]WaitReport(nil), in.Waits...)
	out.ClipTimeline = cloneClipTimeline(in.ClipTimeline)
	if in.Children != nil {
		c := *in.Children
		out.Children = &c
	}
	return &out
}
func (r *Run) JSON() ([]byte, error) {
	p := r.Report()
	if p == nil {
		return nil, nil
	}
	return json.Marshal(p)
}

// RecordWait records a typed blocked interval. BlockedMs is derived from
// the union of all recorded wait intervals, so overlapping waits count once.
func (r *Run) RecordWait(info WaitInfo) {
	if r == nil || info.Kind == "" || info.StartedAt.IsZero() || info.FinishedAt.IsZero() || info.FinishedAt.Before(info.StartedAt) {
		return
	}
	wait := WaitReport{
		Kind:       info.Kind,
		Component:  string(info.Component),
		StartedAt:  info.StartedAt,
		FinishedAt: info.FinishedAt,
		DurationMs: info.FinishedAt.Sub(info.StartedAt).Milliseconds(),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.Waits = append(r.report.Waits, wait)
	blockedWaits := make([]WaitReport, 0, len(r.report.Waits))
	for _, recorded := range r.report.Waits {
		if recorded.Kind != WaitRetryBackoff {
			blockedWaits = append(blockedWaits, recorded)
		}
	}
	r.report.BlockedMs = blockedIntervalUnion(blockedWaits)
}

// RecordWait is the context-bound form of Run.RecordWait: it records the
// typed blocked interval on the run bound to ctx, or is a no-op when no run
// is bound (instrumentation must never change behaviour).
func RecordWait(ctx context.Context, info WaitInfo) {
	if run := FromContext(ctx); run != nil {
		run.RecordWait(info)
	}
}

func (r *Run) SetRetries(n int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished {
		r.report.Counters.Retries = nonNegative(n)
	}
}
func (r *Run) addCounter(fn func(*RunCounters)) {
	if r == nil || fn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished {
		fn(&r.report.Counters)
	}
}
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
func (r *Run) AddBytesDownloaded(n int64) {
	r.addCounter(func(c *RunCounters) { c.BytesDownloaded += nonNegative(n) })
}
func (r *Run) AddBytesUploaded(n int64) {
	r.addCounter(func(c *RunCounters) { c.BytesUploaded += nonNegative(n) })
}
func (r *Run) IncArtifactCreated() { r.addCounter(func(c *RunCounters) { c.ArtifactsCreated++ }) }
func (r *Run) IncArtifactReused()  { r.addCounter(func(c *RunCounters) { c.ArtifactsReused++ }) }

// RecordKPIMilestone records a pipeline KPI timestamp as a wall-clock offset
// in milliseconds from the run's own started clock. Zero offsets are silently
// ignored ("not reached"). The field name is a pointer into PipelineKPIs.
func (r *Run) RecordKPIMilestone(field string, offsetMs int64) {
	if r == nil || offsetMs <= 0 || field == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	switch field {
	case "generate_first_scene_ready_ms":
		r.report.KPIs.GenerateFirstSceneReadyMs = offsetMs
	case "generate_finished_ms":
		r.report.KPIs.GenerateFinishedMs = offsetMs
	case "tts_first_started_ms":
		r.report.KPIs.TTSFirstStartedMs = offsetMs
	case "render_first_started_ms":
		r.report.KPIs.RenderFirstStartedMs = offsetMs
	case "audio_compile_started_ms":
		r.report.KPIs.AudioCompileStartedMs = offsetMs
	case "audio_compile_finished_ms":
		r.report.KPIs.AudioCompileFinishedMs = offsetMs
	case "docs_publish_started_ms":
		r.report.KPIs.DocsPublishStartedMs = offsetMs
	case "docs_publish_finished_ms":
		r.report.KPIs.DocsPublishFinishedMs = offsetMs
	}
}

// SetKPIs computes and records the pipeline invariant checks on the Run's
// own report. It must be called before Finish so the invariants are
// persisted.
func (r *Run) SetKPIs(kpis PipelineKPIs) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.report.KPIs = kpis
}

// RecordKPIMilestone is the context-bound form: records the milestone on the
// run bound to ctx, or is a no-op when no run is bound.
func RecordKPIMilestone(ctx context.Context, field string, offsetMs int64) {
	if run := FromContext(ctx); run != nil {
		run.RecordKPIMilestone(field, offsetMs)
	}
}

func (r *Run) RegisterChild(child *RunReport) {
	if r == nil || child == nil {
		return
	}
	copyChild := cloneReport(child)
	var persist *RunReport
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	if copyChild.ParentRunID == "" {
		copyChild.ParentRunID = r.report.RunID
	}
	if copyChild.ParentJobID == "" {
		copyChild.ParentJobID = r.report.JobID
	}
	key := copyChild.JobID
	if key == "" {
		key = copyChild.RunID
	}
	if key == "" {
		key = fmt.Sprintf("anonymous-child-%d", len(r.children)+1)
	}
	if r.children == nil {
		r.children = make(map[string]RunReport)
	}
	r.children[key] = *copyChild
	requested, completed, failed, wall := 0, 0, 0, int64(0)
	for _, item := range r.children {
		requested++
		if item.Status == StatusSucceeded {
			completed++
		} else if item.Status == StatusFailed || item.Status == StatusCancelled || item.Status == StatusAbandoned {
			failed++
		}
		wall += item.WallTimeMs
	}
	r.report.Children = &ChildrenSummary{Requested: requested, Completed: completed, Failed: failed, AccumulatedChildMs: wall}
	persist = copyChild
	r.mu.Unlock()
	if recorder, ok := r.rec.(LifecycleRecorder); ok {
		if err := recorder.RecordChild(r.ctx, persist); err != nil {
			r.markRecorderFailure("record_child", err)
		}
	}
}
func (r *Run) AddArtifact(a ArtifactReport) {
	if r == nil {
		return
	}
	if a.ObservationID == "" {
		a.ObservationID = NewObservationID()
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

func (r *Run) recordStage(st StageReport) {
	if st.ObservationID == "" {
		st.ObservationID = NewObservationID()
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	r.report.Stages = append(r.report.Stages, st)
	id := r.report.RunID
	r.mu.Unlock()
	if l, ok := r.rec.(LifecycleRecorder); ok {
		if err := l.AppendStage(r.ctx, id, st); err != nil {
			r.markRecorderFailure("append_stage", err)
		}
	}
}
func (r *Run) recordOperation(op OperationReport) {
	if op.ObservationID == "" {
		op.ObservationID = NewObservationID()
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	r.report.Operations = append(r.report.Operations, op)
	r.operationMs += op.DurationMs
	id := r.report.RunID
	r.mu.Unlock()
	if l, ok := r.rec.(LifecycleRecorder); ok {
		if err := l.AppendOperation(r.ctx, id, op); err != nil {
			r.markRecorderFailure("append_operation", err)
		}
	}
}

func NewRunID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("run_%d_%x", time.Now().UnixNano(), b)
	}
	return fmt.Sprintf("run_%d", time.Now().UnixNano())
}
func NewAttemptID() string     { return persistentID("attempt") }
func NewObservationID() string { return persistentID("observation") }
func persistentID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("%s_%d_%x", prefix, time.Now().UnixNano(), b)
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
func blockedIntervalUnion(waits []WaitReport) int64 {
	if len(waits) == 0 {
		return 0
	}
	intervals := append([]WaitReport(nil), waits...)
	for i := 1; i < len(intervals); i++ {
		for j := i; j > 0 && intervals[j].StartedAt.Before(intervals[j-1].StartedAt); j-- {
			intervals[j], intervals[j-1] = intervals[j-1], intervals[j]
		}
	}
	start, end := intervals[0].StartedAt, intervals[0].FinishedAt
	var total time.Duration
	for _, interval := range intervals[1:] {
		if interval.StartedAt.After(end) {
			total += end.Sub(start)
			start, end = interval.StartedAt, interval.FinishedAt
			continue
		}
		if interval.FinishedAt.After(end) {
			end = interval.FinishedAt
		}
	}
	total += end.Sub(start)
	return nonNegative(total.Milliseconds())
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// stageHeartbeatInterval is the visibility cadence for a long, mostly silent
// stage. It is a LOG interval, not a timeout: the stage may legitimately run
// for many minutes, and the point is that an operator can always see which
// operation it is inside instead of staring at a frozen stage name.
const stageHeartbeatInterval = 30 * time.Second

// stageHeartbeat makes a long-running stage observable while it runs. It
// periodically logs the operation the stage is currently inside and how long
// it has been there, so a batch operator can tell slow work from a stuck run
// (the operation changes as work progresses; a stuck one names its blocker),
// and it annotates every failure with the operation that was blocked — the
// timeout/error path of the stage, in one place.
//
// It is deliberately dumb: no timers re-fire work, no state is persisted. The
// run log is the operator surface, and annotate() is the error surface.
type stageHeartbeat struct {
	mu       sync.Mutex
	log      *zap.Logger
	runID    string
	stage    Stage
	interval time.Duration
	op       string
	opSince  time.Time
	started  time.Time
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	progress func(operation string, stageElapsed, operationElapsed time.Duration)
}

// startStageHeartbeat wires a heartbeat for one stage execution of a run.
// Callers must close() it (defer is fine): close is idempotent.
func (r *Runner) startStageHeartbeat(runID string, stage Stage) *stageHeartbeat {
	log := r.log
	if log == nil {
		log = zap.NewNop()
	}
	var progress func(operation string, stageElapsed, operationElapsed time.Duration)
	if jobStage, ok := jobStageForRunStage(stage); ok {
		progress = func(operation string, stageElapsed, operationElapsed time.Duration) {
			jobID := r.jobIDForRun(runID)
			if jobID == "" {
				return
			}
			floor, _ := jobStageProgressBand(jobStage)
			message := fmt.Sprintf("%s: %s still working (%s)", stage, operation, operationElapsed.Round(time.Second))
			r.writeJobProgress(context.Background(), jobID, jobStage, floor, message, map[string]any{
				"stage": string(jobStage), "operation": operation,
				"stage_elapsed_seconds":     int(stageElapsed.Seconds()),
				"operation_elapsed_seconds": int(operationElapsed.Seconds()),
			})
		}
	}
	return newStageHeartbeatWithProgress(log, runID, stage, stageHeartbeatInterval, progress)
}

// newStageHeartbeat builds a heartbeat with an explicit interval. Tests use a
// short one; production callers use startStageHeartbeat.
func newStageHeartbeat(log *zap.Logger, runID string, stage Stage, interval time.Duration) *stageHeartbeat {
	return newStageHeartbeatWithProgress(log, runID, stage, interval, nil)
}

func newStageHeartbeatWithProgress(log *zap.Logger, runID string, stage Stage, interval time.Duration, progress func(string, time.Duration, time.Duration)) *stageHeartbeat {
	if log == nil {
		log = zap.NewNop()
	}
	if interval <= 0 {
		interval = stageHeartbeatInterval
	}
	h := &stageHeartbeat{
		log: log, runID: runID, stage: stage, interval: interval,
		progress: progress,
		started:  time.Now(), opSince: time.Now(),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go h.loop()
	return h
}

func (h *stageHeartbeat) loop() {
	defer close(h.done)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case now := <-ticker.C:
			operation := h.operation()
			stageElapsed := now.Sub(h.started)
			operationElapsed := now.Sub(h.opSinceSnapshot())
			h.log.Info("stage heartbeat: still working",
				zap.String("run_id", h.runID),
				zap.String("stage", string(h.stage)),
				zap.String("operation", operation),
				zap.Duration("stage_elapsed", stageElapsed.Round(time.Second)),
				zap.Duration("operation_elapsed", operationElapsed.Round(time.Second)))
			if h.progress != nil {
				h.progress(operation, stageElapsed, operationElapsed)
			}
		}
	}
}

// setOp records the operation the stage has entered. Every heartbeat and every
// annotate() after the call names it, so the log always identifies WHERE the
// stage is spending its time.
func (h *stageHeartbeat) setOp(name string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.op = name
	h.opSince = time.Now()
	stageElapsed := time.Since(h.started)
	h.mu.Unlock()
	if h.progress != nil {
		h.progress(name, stageElapsed, 0)
	}
}

// annotate wraps a stage failure with the operation that was blocked at the
// time. This is the timeout/error path of the stage: whatever interrupted it
// (a provider timeout, a canceled context, a decode error) leaves an error that
// names the operation instead of only the stage.
func (h *stageHeartbeat) annotate(err error) error {
	if err == nil || h == nil {
		return err
	}
	h.mu.Lock()
	op := h.op
	since := h.opSince
	h.mu.Unlock()
	if op == "" {
		return fmt.Errorf("stage %s failed after %s: %w",
			h.stage, time.Since(h.started).Round(time.Second), err)
	}
	return fmt.Errorf("stage %s blocked in operation %q for %s: %w",
		h.stage, op, time.Since(since).Round(time.Second), err)
}

// close stops the heartbeat. It is safe to call more than once.
func (h *stageHeartbeat) close() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() { close(h.stop) })
	<-h.done
}

func (h *stageHeartbeat) operation() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.op
}

func (h *stageHeartbeat) opSinceSnapshot() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opSince
}

// ── Job stage relay ──
//
// Stage relay is runner lifecycle: these methods mirror the run ledger onto
// the job_stage_status rows. They live beside the other stage-status code so
// the scripts package keeps its committed hotspot baseline.

// jobStageState tracks which job-level stage is currently open for a durable
// run so the next transition can close it.
type jobStageState struct {
	jobID string
	open  job.StageName

	// progressMu fences the monotonic projection onto jobs.progress. TTS and
	// render fan-outs overlap (a render starts as soon as its language's TTS
	// is final) and a retry re-opens a stage at its band floor, so a write
	// from an earlier stage can arrive after a later one already advanced.
	progressMu   sync.Mutex
	lastProgress int
}

// monotoneProgress keeps jobs.progress from moving backwards when concurrent
// stages race, while still allowing a stale stage to refresh current_detail.
// The stage/detail is useful diagnostic state even when its percentage is
// below the aggregate percentage already reported by a later stage.
func (s *jobStageState) monotoneProgress(progress int) int {
	if s == nil {
		return progress
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if progress > s.lastProgress {
		s.lastProgress = progress
	}
	return s.lastProgress
}

// jobIDForRun resolves the broker job bound to a durable run. Map-render
// progress callbacks receive the run identity, but broker updates are keyed
// by job ID.
func (r *Runner) jobIDForRun(runID string) string {
	if r == nil || runID == "" {
		return ""
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return ""
	}
	state, ok := raw.(*jobStageState)
	if !ok || state == nil {
		return ""
	}
	return state.jobID
}

// jobStageStateForJob returns the live stage state that owns jobID, if any.
func (r *Runner) jobStageStateForJob(jobID string) *jobStageState {
	if r == nil || jobID == "" {
		return nil
	}
	var found *jobStageState
	r.jobStages.Range(func(_, value any) bool {
		state, ok := value.(*jobStageState)
		if ok && state != nil && state.jobID == jobID {
			found = state
			return false
		}
		return true
	})
	return found
}

// SetJobStageSink wires the durable job stage-row store. Optional: nil keeps
// the run ledger as the only progress record.
func (r *Runner) SetJobStageSink(sink job.JobStageStatusStore) {
	if r != nil {
		r.jobStageSink = sink
	}
}

// JobProgressSink is the broker's aggregate progress surface (jobs.progress).
// The stage-row store explains WHERE a run is; this sink is what the operator's
// /api/jobs/{id}/full percentage reads. They are separate ports because a
// deployment may persist stage rows without exposing structured progress.
type JobProgressSink interface {
	SetProgress(ctx context.Context, id string, progress int, message string) error
}

// JobProgressDataSink is the optional structured variant. When the broker repo
// implements it, progress carries the stage/completed/total activity payload
// instead of only a percentage.
type JobProgressDataSink interface {
	SetProgressData(ctx context.Context, id string, progress int, message string, data map[string]any) error
}

// SetJobProgressSink wires the broker aggregate-progress store. Optional: nil
// falls back to the run ledger and job_stage_status only.
func (r *Runner) SetJobProgressSink(sink JobProgressSink) {
	if r != nil {
		r.jobProgressSink = sink
	}
}

// bindJobStage associates a durable run with the job whose stage rows it feeds.
func (r *Runner) bindJobStage(runID, jobID string) {
	if r == nil || r.jobStageSink == nil || runID == "" || jobID == "" {
		return
	}
	r.jobStages.Store(runID, &jobStageState{jobID: jobID})
}

// closeJobStage closes the job stage still open when the execution returns.
// The run's terminal status decides the outcome: COMPLETED closes it as
// completed, anything else as failed. Best-effort, like every relay write.
func (r *Runner) closeJobStage(ctx context.Context, runID string) {
	if r == nil || r.jobStageSink == nil {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state := raw.(*jobStageState)
	if state.open == "" {
		return
	}
	status := job.StageFailed
	if run, err := r.repo.Get(ctx, runID); err == nil && run != nil && run.Status == RunStatusCompleted {
		status = job.StageCompleted
	}
	r.writeJobStage(ctx, state.jobID, state.open, status, time.Now().UTC())
	state.open = ""
}

// unbindJobStage removes the association once the execution has returned.
func (r *Runner) unbindJobStage(runID string) {
	if r == nil {
		return
	}
	r.jobStages.Delete(runID)
}

// jobStageForRunStage maps a run-ledger stage onto the job stage vocabulary.
// Stages without a job-level equivalent return false and are not relayed.
func jobStageForRunStage(stage Stage) (job.StageName, bool) {
	switch stage {
	case StageNormalizing, StagePreflight, StageGeneratingSceneText:
		return job.StageScript, true
	case StageTranslatingScenes:
		return job.StageTranslation, true
	case StageGeneratingVoiceovers:
		return job.StageVoiceover, true
	case StageRenderingVideo:
		// The localized render fan-out is dispatched from the voiceover
		// phase but reports as the render stage on the job surface.
		return job.StageRender, true
	case StageCompilingAudio:
		// Overlay rendering, map rendering and audio compilation all complete
		// inside this durable stage before the final job is submitted.
		return job.StageRender, true
	case StagePublishingDocuments:
		// Legacy name; the phase now owns the remote final job and Drive
		// delivery. upload is the closest canonical job stage.
		return job.StageUpload, true
	}
	return "", false
}

// jobStageProgressBand maps a job stage onto the aggregate-progress interval
// it owns. The bands are monotone and disjoint so a stage transition can never
// make jobs.progress move backwards.
func jobStageProgressBand(stage job.StageName) (floor, ceiling int) {
	switch stage {
	case job.StageScript:
		return 5, 25
	case job.StageClips, job.StageStock:
		return 20, 30
	case job.StageTranslation:
		return 25, 40
	case job.StageVoiceover:
		return 40, 60
	case job.StageOverlay:
		return 60, 70
	case job.StageRender:
		return 70, 85
	case job.StageUpload:
		return 85, 95
	case job.StagePersistence:
		return 95, 99
	default:
		return 0, 99
	}
}

// relayJobStage writes the job stage row for a run-ledger transition. It is
// best-effort: a failed row write is logged and never fails the run.
func (r *Runner) relayJobStage(ctx context.Context, runID string, stage Stage) {
	if r == nil || r.jobStageSink == nil {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state := raw.(*jobStageState)
	closing := stage == StageCoreReady || stage == StageCompleted
	next, opens := jobStageForRunStage(stage)
	if !opens && !closing {
		return
	}
	now := time.Now().UTC()
	if state.open != "" && (closing || (opens && next != state.open)) {
		r.writeJobStage(ctx, state.jobID, state.open, job.StageCompleted, now)
		state.open = ""
	}
	if opens {
		r.writeJobStage(ctx, state.jobID, next, job.StageRunning, now)
		state.open = next
	}
}

func (r *Runner) writeJobStage(ctx context.Context, jobID string, stage job.StageName, status job.StageStatus, at time.Time) {
	progress := 0
	if status == job.StageCompleted {
		progress = 100
	}
	rec := job.JobStageStatus{JobID: jobID, Stage: stage, Status: status, Progress: progress, UpdatedAt: at}
	if err := r.jobStageSink.UpsertJobStageStatus(ctx, rec); err != nil && r.log != nil {
		r.log.Warn("job stage status write failed",
			zap.String("job_id", jobID),
			zap.String("stage", string(stage)),
			zap.Error(err))
	}
	floor, ceiling := jobStageProgressBand(stage)
	aggregate := floor
	if status == job.StageCompleted {
		aggregate = ceiling
	}
	r.writeJobProgress(ctx, jobID, stage, aggregate, string(stage)+" "+string(status), nil)
}

// writeJobStageProgress projects the live fan-out counter onto the durable
// job_stage_status row. The stage relay alone only knows open/closed, so a
// voiceover stage with hundreds of TTS items stayed at progress=0 for its
// whole run (the r14-r16 "parent progress frozen" report). The counter is
// best-effort: a sink failure is logged, never allowed to fail the run.
func (r *Runner) writeJobStageProgress(ctx context.Context, runID string, stage job.StageName, completed, total int) {
	if r == nil || total <= 0 {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state, ok := raw.(*jobStageState)
	if !ok || state == nil || state.jobID == "" {
		return
	}
	pct := completed * 100 / total
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if r.jobStageSink != nil {
		rec := job.JobStageStatus{
			JobID: state.jobID, Stage: stage, Status: job.StageRunning, Progress: pct,
			Detail: fmt.Sprintf("%d/%d", completed, total), UpdatedAt: time.Now().UTC(),
		}
		if err := r.jobStageSink.UpsertJobStageStatus(ctx, rec); err != nil && r.log != nil {
			r.log.Warn("job stage progress write failed",
				zap.String("job_id", state.jobID),
				zap.String("stage", string(stage)),
				zap.Int("completed", completed),
				zap.Int("total", total),
				zap.Error(err))
		}
	}
	floor, ceiling := jobStageProgressBand(stage)
	aggregate := floor + (ceiling-floor)*pct/100
	if aggregate < floor {
		aggregate = floor
	}
	if aggregate > ceiling {
		aggregate = ceiling
	}
	r.writeJobProgress(ctx, state.jobID, stage, aggregate, fmt.Sprintf("%s %d/%d", stage, completed, total), map[string]any{
		"stage": string(stage), "completed": completed, "total": total,
	})
}

// writeJobProgress is the single projection onto jobs.progress. The structured
// variant is preferred; the plain variant keeps deployments that only expose
// the legacy SetProgress port working. A sink error is logged, never fatal: a
// progress write must not fail a render.
func (r *Runner) writeJobProgress(ctx context.Context, jobID string, stage job.StageName, progress int, message string, data map[string]any) {
	if r == nil || r.jobProgressSink == nil || jobID == "" {
		return
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	// Overlapping TTS/render fan-outs and re-opened stages can write an older
	// band after a newer one. Keep the aggregate monotone but persist the latest
	// message/data so a map heartbeat or a late stage failure remains visible.
	if state := r.jobStageStateForJob(jobID); state != nil {
		progress = state.monotoneProgress(progress)
	}
	var err error
	if structured, ok := r.jobProgressSink.(JobProgressDataSink); ok && data != nil {
		err = structured.SetProgressData(ctx, jobID, progress, message, data)
	} else {
		err = r.jobProgressSink.SetProgress(ctx, jobID, progress, message)
	}
	if err != nil && r.log != nil {
		r.log.Warn("job aggregate progress write failed",
			zap.String("job_id", jobID),
			zap.String("stage", string(stage)),
			zap.Int("progress", progress),
			zap.Error(err))
	}
}

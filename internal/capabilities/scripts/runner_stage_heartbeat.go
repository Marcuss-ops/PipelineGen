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
}

// startStageHeartbeat wires a heartbeat for one stage execution of a run.
// Callers must close() it (defer is fine): close is idempotent.
func (r *Runner) startStageHeartbeat(runID string, stage Stage) *stageHeartbeat {
	log := r.log
	if log == nil {
		log = zap.NewNop()
	}
	return newStageHeartbeat(log, runID, stage, stageHeartbeatInterval)
}

// newStageHeartbeat builds a heartbeat with an explicit interval. Tests use a
// short one; production callers use startStageHeartbeat.
func newStageHeartbeat(log *zap.Logger, runID string, stage Stage, interval time.Duration) *stageHeartbeat {
	if log == nil {
		log = zap.NewNop()
	}
	if interval <= 0 {
		interval = stageHeartbeatInterval
	}
	h := &stageHeartbeat{
		log: log, runID: runID, stage: stage, interval: interval,
		started: time.Now(), opSince: time.Now(),
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
			h.log.Info("stage heartbeat: still working",
				zap.String("run_id", h.runID),
				zap.String("stage", string(h.stage)),
				zap.String("operation", h.operation()),
				zap.Duration("stage_elapsed", now.Sub(h.started).Round(time.Second)),
				zap.Duration("operation_elapsed", now.Sub(h.opSinceSnapshot()).Round(time.Second)))
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
	h.mu.Unlock()
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
}

// SetJobStageSink wires the durable job stage-row store. Optional: nil keeps
// the run ledger as the only progress record.
func (r *Runner) SetJobStageSink(sink job.JobStageStatusStore) {
	if r != nil {
		r.jobStageSink = sink
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
	}
	return "", false
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
	rec := job.JobStageStatus{JobID: jobID, Stage: stage, Status: status, UpdatedAt: at}
	if err := r.jobStageSink.UpsertJobStageStatus(ctx, rec); err != nil && r.log != nil {
		r.log.Warn("job stage status write failed",
			zap.String("job_id", jobID),
			zap.String("stage", string(stage)),
			zap.Error(err))
	}
}

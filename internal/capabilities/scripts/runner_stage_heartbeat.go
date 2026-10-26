package scriptgeneration

import (
	"fmt"
	"sync"
	"time"

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

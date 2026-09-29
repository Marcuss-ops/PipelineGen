// Package app — voiceover Drive-upload gate tests (2026-09-28).
//
// These tests pin the two properties the 2026-09-28 measurement asked for:
//
//  1. the wait for a Drive-upload slot is recorded as a typed WaitSemaphore
//     interval on the run, so it stops being charged to the upload's work time;
//  2. a job that is starving for a slot is served before the releasing job's
//     own queued upload, so one job can no longer monopolize the shared gate.
package voiceover

import (
	"context"
	"sync"
	"testing"
	"time"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	obs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// gatePublisher is a VoiceoverPublisher test double that records the START
// order of calls (i.e. the order in which they won a gate slot) and holds every
// call until the test opens its per-call gate.
type gatePublisher struct {
	mu      sync.Mutex
	started []string
	gates   map[string]chan struct{}
}

func newGatePublisher() *gatePublisher {
	return &gatePublisher{gates: make(map[string]chan struct{})}
}

func (g *gatePublisher) Publish(_ context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	g.mu.Lock()
	gate := make(chan struct{})
	g.gates[cmd.ID] = gate
	g.started = append(g.started, cmd.ID)
	g.mu.Unlock()
	<-gate
	return "file-" + cmd.ID, nil
}

func (g *gatePublisher) open(id string) {
	g.mu.Lock()
	gate := g.gates[id]
	g.mu.Unlock()
	if gate != nil {
		close(gate)
	}
}

func (g *gatePublisher) startedIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.started...)
}

func (g *gatePublisher) waitStarted(t *testing.T, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, started := range g.startedIDs() {
			if started == id {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("publish %q never reached the gate; started=%v", id, g.startedIDs())
}

func driveGateConfig(capacity int) config.VoiceoverConcurrencyConfig {
	return config.VoiceoverConcurrencyConfig{
		MaxConcurrentDriveUploads: capacity,
		DriveUploadTimeoutSec:     5,
		DriveUploadMaxRetries:     1,
		DriveUploadRetryBackoffMs: 10,
	}
}

func boundRunContext(t *testing.T, runID string) (context.Context, *obs.Run) {
	t.Helper()
	run := obs.NewRunObserver(nil).StartRun(context.Background(), obs.RunInfo{
		RunID: runID, JobID: runID + "-job", JobType: "script.generate", AttemptID: runID + "-attempt",
	})
	if run == nil {
		t.Fatal("run observer returned a nil run")
	}
	t.Cleanup(func() { _ = run.Finish() })
	return obs.WithRun(context.Background(), run), run
}

// TestRateLimitedPublisher_RecordsDriveQueueWait pins requirement (1): the
// waiting upload's blocked time is recorded on the run as a semaphore wait, so
// the timing report can tell "waited for a slot" apart from "slow upload".
func TestRateLimitedPublisher_RecordsDriveQueueWait(t *testing.T) {
	inner := newGatePublisher()
	publisher := NewRateLimitedPublisher(inner, driveGateConfig(1), zap.NewNop())
	ctx, run := boundRunContext(t, "job-holder")

	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		if _, err := publisher.Publish(ctx, voiceover.VoiceoverPublishCommand{ID: "hold", Filename: "a.mp3"}); err != nil {
			t.Errorf("holder publish: %v", err)
		}
	}()
	inner.waitStarted(t, "hold", time.Second)

	waiterDone := make(chan error, 1)
	go func() {
		_, err := publisher.Publish(ctx, voiceover.VoiceoverPublishCommand{ID: "wait", Filename: "b.mp3"})
		waiterDone <- err
	}()
	time.Sleep(30 * time.Millisecond)

	inner.open("hold")
	select {
	case <-holderDone:
	case <-time.After(time.Second):
		t.Fatal("holder publish never completed")
	}
	// The waiter now owns the slot: its own upload must be released too, so the
	// call can finish and the recorded wait can be asserted.
	inner.waitStarted(t, "wait", time.Second)
	inner.open("wait")
	select {
	case err := <-waiterDone:
		if err != nil {
			t.Fatalf("waiter publish: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter publish never completed")
	}

	var driveWaits int
	var waitedMs int64
	for _, wait := range run.Report().Waits {
		if wait.Kind == obs.WaitSemaphore && wait.Component == string(obs.ComponentDrive) {
			driveWaits++
			waitedMs += wait.DurationMs
		}
	}
	if driveWaits == 0 {
		t.Fatalf("no %s/%s wait recorded; waits=%+v", obs.WaitSemaphore, obs.ComponentDrive, run.Report().Waits)
	}
	if waitedMs < 20 {
		t.Fatalf("drive queue wait = %dms, want >= 20ms (the waiter blocked on the holder)", waitedMs)
	}
	if run.Report().BlockedMs < 20 {
		t.Fatalf("run blocked_ms = %dms, want the drive queue wait to count as blocked", run.Report().BlockedMs)
	}
}

// TestRateLimitedPublisher_FairnessAcrossJobs pins requirement (2): with the
// shared gate full and two jobs queued, releasing a slot serves the STARVING
// job instead of the releasing job's own next upload. This is the regression
// that produced an 85.7 s final_audio upload on 2026-09-28.
func TestRateLimitedPublisher_FairnessAcrossJobs(t *testing.T) {
	inner := newGatePublisher()
	publisher := NewRateLimitedPublisher(inner, driveGateConfig(2), zap.NewNop())
	ctxA, _ := boundRunContext(t, "job-a")
	ctxB, _ := boundRunContext(t, "job-b")

	// job-a holds BOTH slots with two pipelined uploads.
	for _, id := range []string{"a1", "a2"} {
		id := id
		go func() {
			if _, err := publisher.Publish(ctxA, voiceover.VoiceoverPublishCommand{ID: id, Filename: id + ".mp3"}); err != nil {
				t.Errorf("%s publish: %v", id, err)
			}
		}()
		inner.waitStarted(t, id, time.Second)
	}

	// job-a pipelines a third upload, then job-b queues a single upload.
	var a3Done sync.WaitGroup
	a3Done.Add(1)
	go func() {
		defer a3Done.Done()
		if _, err := publisher.Publish(ctxA, voiceover.VoiceoverPublishCommand{ID: "a3", Filename: "a3.mp3"}); err != nil {
			t.Errorf("a3 publish: %v", err)
		}
	}()
	b1Done := make(chan struct{})
	go func() {
		defer close(b1Done)
		if _, err := publisher.Publish(ctxB, voiceover.VoiceoverPublishCommand{ID: "b1", Filename: "b1.mp3"}); err != nil {
			t.Errorf("b1 publish: %v", err)
		}
	}()
	time.Sleep(30 * time.Millisecond)

	// Free one slot: it must go to job-b, the starving owner.
	inner.open("a1")
	inner.waitStarted(t, "b1", time.Second)
	for _, started := range inner.startedIDs() {
		if started == "a3" {
			t.Fatalf("job-a's queued upload won the slot while job-b was starving; started=%v", inner.startedIDs())
		}
	}

	// Only after job-b's upload releases does job-a's queued upload run.
	inner.open("b1")
	select {
	case <-b1Done:
	case <-time.After(time.Second):
		t.Fatal("b1 never completed")
	}
	inner.waitStarted(t, "a3", time.Second)

	inner.open("a2")
	inner.open("a3")
	done := make(chan struct{})
	go func() {
		a3Done.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a3 never completed")
	}
}

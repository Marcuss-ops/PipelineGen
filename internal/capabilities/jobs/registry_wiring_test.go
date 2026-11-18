// Package jobs — registry_wiring_test.go (Issue 2 / P0, June 2026).
//
// Pins the in-process Runner → Worker jobscheduling.Registry plumbing contract:
//
//   - TestWorker_HonorsRegistryTimeout  — Worker.WithRegistry(reg)
//     makes jobTimeoutFor read reg.Timeout instead of falling back
//     to the literal 10-min default.
//   - TestRunner_AttachesRegistryToWorkers — Runner.buildWorkers
//     propagates the attached jobscheduling.Registry onto every spawned Worker;
//     the timeouts snapshot (reg.Compose()) is bound to each Worker.
//   - TestRunner_WithRegistry_NilIsTolerant — Runner.WithRegistry(nil)
//     leaves registry=nil and propagates nil to the workers, so test
//     fixtures that opt out of registry attachment keep working.
//
// This file uses package `jobs` directly (internal test) so it can
// reach private fields (`Worker.reg`, `Worker.timeouts`) without
// exposing new public surface. Production surface stays the same;
// the test is internal-only.
package jobs

import (
	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	"testing"
	"time"

	"go.uber.org/zap"
)

const wiringTestType = "wiring-test-type"

// newWiringRegistry builds an isolated jobscheduling.Registry with the timeout /
// retries overrides needed by the tests. Returns a private registry
// (not jobs.Compose()) so the tests don't mutate the shared global.
func newWiringRegistry(t *testing.T, timeout time.Duration, maxRetries int) *jobscheduling.Registry {
	t.Helper()
	reg := jobscheduling.NewRegistry()
	if err := reg.Register(jobscheduling.RegistryEntry{
		Completion: jobscheduling.CompletionDeclaration{
			JobType:              wiringTestType,
			ArtifactOwnership:    jobscheduling.ArtifactOwnershipNone,
			FinalizationStrategy: jobscheduling.FinalizationStrategyLegacyComplete,
		},
		Description:       "wiring-test entry",
		Timeout:           timeout,
		DefaultMaxRetries: maxRetries,
	}); err != nil {
		t.Fatalf("register %s: %v", wiringTestType, err)
	}
	return reg
}

// TestWorker_HonorsRegistryTimeout — Issue 2 / P0 timeout contract.
func TestWorker_HonorsRegistryTimeout(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 4*time.Minute, 5)

	w := NewWorker(WorkerDeps{
		ID:         "honors-timeout-worker",
		Repo:       nil, // repo (unused — pure helper test)
		Dispatcher: NewDispatcher(),
		Notifier:   nil, // notifier (unused — pure helper test)
		Log:        zap.NewNop(),
		LeaseTTL:   5 * time.Minute, // leaseTTL
		PollEvery:  2 * time.Second, // pollEvery
		Backoff: BackoffConfig{
			MaxBackoff:                60 * time.Second,
			JitterFraction:            0,
			ConsecutiveEmptyThreshold: 0,
		},
		Types: []string{wiringTestType},
	}).WithRegistry(reg)

	got := w.jobTimeoutFor(wiringTestType)
	if got != 4*time.Minute {
		t.Errorf("Issue 2 / P0: Worker.jobTimeoutFor(%q) with attached jobscheduling.Registry: got %v, want %v",
			wiringTestType, got, 4*time.Minute)
	}
}

// TestRunner_AttachesRegistryToWorkers — Issue 2 / P0 wiring contract.
// Runner.buildWorkers must propagate the attached jobscheduling.Registry onto
// every spawned Worker; the timeouts snapshot must be populated
// from reg.Compose().
//
// We use buildWorkers directly (not Start) so the test is
// deterministic and does not depend on goroutine scheduling. Start
// itself is exercised in
// internal/app/lifecycle_job_runner.go::buildJobRunner which has
// its own integration coverage.
func TestRunner_AttachesRegistryToWorkers(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 4*time.Minute, 5)

	const poolSize = 3
	runner := NewRunner(
		nil, // repo (unused — wiring-only test)
		nil, // dispatcher (unused — wiring-only test)
		zap.NewNop(),
		RunnerConfig{
			Workers:   poolSize,
			PollEvery: 2 * time.Second,
			LeaseTTL:  5 * time.Minute,
			Backoff: BackoffConfig{
				MaxBackoff:                60 * time.Second,
				JitterFraction:            0,
				ConsecutiveEmptyThreshold: 0,
			},
			Notifier: nil, // unused by buildWorkers
		},
	).WithRegistry(reg)

	workers := runner.buildWorkers()
	if len(workers) != poolSize {
		t.Fatalf("buildWorkers: got %d workers, want %d", len(workers), poolSize)
	}
	for i, w := range workers {
		if w.reg != reg {
			t.Errorf("worker[%d] Worker.reg: not the jobscheduling.Registry attached to Runner", i)
		}
		if w.timeouts == nil {
			t.Errorf("worker[%d] Worker.timeouts snapshot is nil; expected reg.Compose()", i)
			continue
		}
		if d, ok := w.timeouts[wiringTestType]; !ok || d != 4*time.Minute {
			t.Errorf("worker[%d] Worker.timeouts[%q]: got %v (ok=%t), want 4m", i, wiringTestType, d, ok)
		}
	}
}

// TestRunner_WithRegistry_NilIsTolerant — preserves test fixtures
// that opt out of registry attachment. Worker.WithRegistry(nil) was
// already nil-tolerant (HC-1 June 2026); Runner.WithRegistry(nil)
// must be likewise (this test pins the contract).
func TestRunner_WithRegistry_NilIsTolerant(t *testing.T) {
	t.Parallel()
	runner := NewRunner(
		nil,
		nil,
		zap.NewNop(),
		RunnerConfig{
			Workers:   1,
			PollEvery: 2 * time.Second,
			LeaseTTL:  5 * time.Minute,
			Backoff: BackoffConfig{
				MaxBackoff:                60 * time.Second,
				ConsecutiveEmptyThreshold: 0,
			},
		},
	).WithRegistry(nil)

	if runner.reg != nil {
		t.Errorf("Runner.WithRegistry(nil) should leave registry=nil, got %v", runner.reg)
	}
	workers := runner.buildWorkers()
	if len(workers) != 1 {
		t.Fatalf("got %d workers, want 1", len(workers))
	}
	if workers[0].reg != nil {
		t.Errorf("worker[0] reg should be nil when Runner.registry is nil, got %v", workers[0].reg)
	}
}

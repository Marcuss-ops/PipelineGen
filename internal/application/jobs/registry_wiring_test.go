// Package jobs — registry_wiring_test.go (Issue 2 / P0, June 2026).
//
// Pins the in-process Runner → Worker Registry plumbing contract:
//
//   - TestWorker_HonorsRegistryTimeout  — Worker.WithRegistry(reg)
//     makes jobTimeoutFor read reg.Timeout instead of falling back
//     to the literal 10-min default.
//   - TestWorker_HonorsRegistryRetries  — Worker.WithRegistry(reg)
//     makes maxRetriesFor read reg.DefaultMaxRetries instead of
//     falling back to the literal 3.
//   - TestRunner_AttachesRegistryToWorkers — Runner.buildWorkers
//     propagates the attached Registry onto every spawned Worker;
//     the timeouts snapshot (reg.Compose()) is bound to each Worker.
//   - TestRunner_WithRegistry_NilIsTolerant — Runner.WithRegistry(nil)
//     leaves registry=nil and propagates nil to the workers, so test
//     fixtures that opt out of registry attachment keep working.
//
// This file uses package `jobs` directly (internal test) so it can
// reach private fields (`Worker.reg`, `Worker.timeouts`) without
// exposing new public surface. Production surface stays the same;
// the test is internal-only.
//
// Issue 4 (P1, Enqueue path) will swap runJob's
// `j.RetryCount < j.MaxRetries` check to consult
// `w.maxRetriesFor(j.Type)` — the helper exists today and the
// TestWorker_HonorsRegistryRetries pin ensures drift cannot regress
// the lookup surface.
package jobs

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

const wiringTestType = "wiring-test-type"

// newWiringRegistry builds an isolated Registry with the timeout /
// retries overrides needed by the tests. Returns a private registry
// (not jobs.Compose()) so the tests don't mutate the shared global.
func newWiringRegistry(t *testing.T, timeout time.Duration, maxRetries int) *Registry {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(RegistryEntry{
		Completion: CompletionDeclaration{
			JobType:              wiringTestType,
			ArtifactOwnership:    ArtifactOwnershipNone,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
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
		t.Errorf("Issue 2 / P0: Worker.jobTimeoutFor(%q) with attached Registry: got %v, want %v",
			wiringTestType, got, 4*time.Minute)
	}
}

// TestWorker_HonorsRegistryRetries — Issue 2 / P0 retry helper
// contract. The runJob integration is Issue 4 (P1) territory; this
// test pins the helper so Issue 4 can swap-in one line.
func TestWorker_HonorsRegistryRetries(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, 4*time.Minute, 5)

	w := NewWorker(WorkerDeps{
		ID:         "honors-retries-worker",
		Repo:       nil,
		Dispatcher: NewDispatcher(),
		Notifier:   nil,
		Log:        zap.NewNop(),
		LeaseTTL:   5 * time.Minute,
		PollEvery:  2 * time.Second,
		Backoff: BackoffConfig{
			MaxBackoff:                60 * time.Second,
			ConsecutiveEmptyThreshold: 0,
		},
		Types: []string{wiringTestType},
	}).WithRegistry(reg)

	// Registered type → Registry.DefaultMaxRetries.
	if got := w.maxRetriesFor(wiringTestType); got != 5 {
		t.Errorf("Issue 2 / P0: Worker.maxRetriesFor(%q) with attached Registry: got %d, want %d",
			wiringTestType, got, 5)
	}

	// Unregistered type → Registry returns the canonical 3.
	if got := w.maxRetriesFor("not-a-real-type"); got != 3 {
		t.Errorf("Worker.maxRetriesFor(unregistered): got %d, want canonical 3", got)
	}
}

// TestRunner_AttachesRegistryToWorkers — Issue 2 / P0 wiring contract.
// Runner.buildWorkers must propagate the attached Registry onto
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
			t.Errorf("worker[%d] Worker.reg: not the Registry attached to Runner", i)
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

	if runner.registry != nil {
		t.Errorf("Runner.WithRegistry(nil) should leave registry=nil, got %v", runner.registry)
	}
	workers := runner.buildWorkers()
	if len(workers) != 1 {
		t.Fatalf("got %d workers, want 1", len(workers))
	}
	if workers[0].reg != nil {
		t.Errorf("worker[0] reg should be nil when Runner.registry is nil, got %v", workers[0].reg)
	}
}

// ── Issue 4 (June 2026, P1) + PR-jobs-retry-contract (July 2026) ───
// Pin the *Service*-side strict typed MaxRetries resolution. The
// PR-jobs-retry-contract refactor removes the pre-PR legacy
// hard-coded 3-retry fallback (godlike/07 NO-FAKE-AVAILABILITY) and
// replaces it with a single typed Registry.GetMaxRetries(jobType)
// lookup that returns ErrMaxRetriesUnknown for unregistered types.
// resolveMaxRetries signature is now (int, error) — the caller
// (Enqueue) propagates the error so a missing registration is loud,
// NOT silenced by a silent default.
//
// Other split of duties (helper layer for EnqueueGenerationJob): pinned
// in internal/capabilities/scripts/jobs/generation_enqueue_registry_test.go.

// TestService_ResolveMaxRetries_RegisteredTypeUsesRegistryDefault:
// PR-jobs-retry-contract strict typed contract — when a registry is
// attached AND the job type is registered, MaxRetries=0 must resolve
// to registry.DefaultMaxRetries (5 in this fixture) via the typed
// GetMaxRetries lookup. Returned error MUST be nil.
func TestService_ResolveMaxRetries_RegisteredTypeUsesRegistryDefault(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 5)

	svc, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.resolveMaxRetries(wiringTestType, 0)
	if err != nil {
		t.Errorf("PR-jobs-retry-contract: resolveMaxRetries(registered, 0) err = %v, want nil", err)
	}
	if got != 5 {
		t.Errorf("resolveMaxRetries(registered, 0) = %d, want registry default 5", got)
	}
}

// TestService_ResolveMaxRetries_UnregisteredType_ReturnsErrMaxRetriesUnknown:
// strict typed-error contract — unregistered jobTypes MUST return
// ErrMaxRetriesUnknown (godlike/07 NO-FAKE-AVAILABILITY: the legacy
// silent 3-retry fallback is REMOVED in PR-jobs-retry-contract).
func TestService_ResolveMaxRetries_UnregisteredType_ReturnsErrMaxRetriesUnknown(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 5)

	svc, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.resolveMaxRetries("totally.unknown.type", 0)
	if !errors.Is(err, ErrMaxRetriesUnknown) {
		t.Errorf("resolveMaxRetries(unregistered, 0) err = %v, want ErrMaxRetriesUnknown", err)
	}
	if got != 0 {
		t.Errorf("resolveMaxRetries(unregistered, 0) = %d, want 0 on error path", got)
	}
}

// TestService_ResolveMaxRetries_PreservesExplicitValue: callers may
// pre-set MaxRetries to an explicit value (e.g. admin override, batch
// retry policy). The registry MUST NOT override that value.
func TestService_ResolveMaxRetries_PreservesExplicitValue(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 5)
	svc, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for _, v := range []int{1, 2, 4, 7, 100} {
		got, err := svc.resolveMaxRetries(wiringTestType, v)
		if err != nil {
			t.Errorf("resolveMaxRetries(%q, %d) err = %v, want nil", wiringTestType, v, err)
		}
		if got != v {
			t.Errorf("resolveMaxRetries(%q, %d) = %d, want preserved verbatim", wiringTestType, v, got)
		}
	}
}

// TestService_ResolveMaxRetries_PreservesNegativeSentinel: -1 was the
// canonical "explicit zero retries" sentinel in the pre-Issue-4 code
// path. The new strict typed lookup MUST preserve that semantic
// verbatim — -1 must NOT be silently turned into "use default" nor
// "lookup registry default".
func TestService_ResolveMaxRetries_PreservesNegativeSentinel(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 5)
	svc, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	for _, v := range []int{-1, -7, -42} {
		got, err := svc.resolveMaxRetries(wiringTestType, v)
		if err != nil {
			t.Errorf("resolveMaxRetries(%q, %d) err = %v, want nil", wiringTestType, v, err)
		}
		if got != 0 {
			t.Errorf("resolveMaxRetries(%q, %d) = %d, want 0 (negative sentinel maps to no-retries)", wiringTestType, v, got)
		}
	}
}

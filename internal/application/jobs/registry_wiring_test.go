package jobs

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

const wiringTestType = "wiring-test-type"

func newWiringRegistry(t *testing.T, timeout time.Duration, maxRetries int) *Registry {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register(RegistryEntry{
		Type:              wiringTestType,
		Description:       "wiring-test entry",
		Timeout:           timeout,
		DefaultMaxRetries: maxRetries,
	}); err != nil {
		t.Fatalf("register %s: %v", wiringTestType, err)
	}
	return registry
}

func newRegistryTestWorker(id string) *Worker {
	return NewWorkerFromDeps(WorkerDeps{
		Identity: WorkerIdentityDeps{
			ID:       id,
			JobTypes: []string{wiringTestType},
		},
		Runtime: WorkerRuntimeDeps{Dispatcher: NewDispatcher()},
		Timing: WorkerTimingDeps{
			LeaseTTL:  5 * time.Minute,
			PollEvery: 2 * time.Second,
			Backoff: BackoffConfig{
				MaxBackoff: 60 * time.Second,
			},
		},
		Log: zap.NewNop(),
	})
}

func TestWorker_HonorsRegistryTimeout(t *testing.T) {
	registry := newWiringRegistry(t, 4*time.Minute, 5)
	worker := newRegistryTestWorker("honors-timeout-worker").WithRegistry(registry)
	if got := worker.jobTimeoutFor(wiringTestType); got != 4*time.Minute {
		t.Fatalf("jobTimeoutFor=%v, want 4m", got)
	}
}

func TestWorker_HonorsRegistryRetries(t *testing.T) {
	registry := newWiringRegistry(t, 4*time.Minute, 5)
	worker := newRegistryTestWorker("honors-retries-worker").WithRegistry(registry)
	if got := worker.maxRetriesFor(wiringTestType); got != 5 {
		t.Fatalf("maxRetriesFor registered=%d, want 5", got)
	}
	if got := worker.maxRetriesFor("not-a-real-type"); got != 3 {
		t.Fatalf("maxRetriesFor unregistered=%d, want 3", got)
	}
}

func TestRunner_AttachesRegistryToWorkers(t *testing.T) {
	registry := newWiringRegistry(t, 4*time.Minute, 5)
	runner := NewRunner(nil, nil, zap.NewNop(), RunnerConfig{
		Workers:   3,
		PollEvery: 2 * time.Second,
		LeaseTTL:  5 * time.Minute,
		Backoff: BackoffConfig{
			MaxBackoff: 60 * time.Second,
		},
	}).WithRegistry(registry)

	workers := runner.buildWorkers()
	if len(workers) != 3 {
		t.Fatalf("workers=%d, want 3", len(workers))
	}
	for i, worker := range workers {
		if worker.reg != registry {
			t.Errorf("worker[%d] registry not propagated", i)
		}
		if duration, ok := worker.timeouts[wiringTestType]; !ok || duration != 4*time.Minute {
			t.Errorf("worker[%d] timeout=%v ok=%v, want 4m", i, duration, ok)
		}
	}
}

func TestRunner_WithRegistry_NilIsTolerant(t *testing.T) {
	runner := NewRunner(nil, nil, zap.NewNop(), RunnerConfig{
		Workers:   1,
		PollEvery: 2 * time.Second,
		LeaseTTL:  5 * time.Minute,
		Backoff: BackoffConfig{
			MaxBackoff: 60 * time.Second,
		},
	}).WithRegistry(nil)
	workers := runner.buildWorkers()
	if runner.registry != nil || len(workers) != 1 || workers[0].reg != nil {
		t.Fatalf("nil registry contract failed: runner=%v workers=%d workerReg=%v", runner.registry, len(workers), workers[0].reg)
	}
}

func TestService_ResolveMaxRetries_RegisteredTypeUsesRegistryDefault(t *testing.T) {
	service, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), newWiringRegistry(t, time.Minute, 5))
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.resolveMaxRetries(wiringTestType, 0)
	if err != nil || got != 5 {
		t.Fatalf("resolveMaxRetries registered=(%d,%v), want (5,nil)", got, err)
	}
}

func TestService_ResolveMaxRetries_UnregisteredType_ReturnsErrMaxRetriesUnknown(t *testing.T) {
	service, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), newWiringRegistry(t, time.Minute, 5))
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.resolveMaxRetries("totally.unknown.type", 0)
	if !errors.Is(err, ErrMaxRetriesUnknown) || got != 0 {
		t.Fatalf("resolveMaxRetries unregistered=(%d,%v), want (0,ErrMaxRetriesUnknown)", got, err)
	}
}

func TestService_ResolveMaxRetries_PreservesExplicitValue(t *testing.T) {
	service, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), newWiringRegistry(t, time.Minute, 5))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []int{1, 2, 4, 7, 100} {
		got, err := service.resolveMaxRetries(wiringTestType, value)
		if err != nil || got != value {
			t.Errorf("resolveMaxRetries(%d)=(%d,%v)", value, got, err)
		}
	}
}

func TestService_ResolveMaxRetries_PreservesNegativeSentinel(t *testing.T) {
	service, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), newWiringRegistry(t, time.Minute, 5))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []int{-1, -7, -42} {
		got, err := service.resolveMaxRetries(wiringTestType, value)
		if err != nil || got != 0 {
			t.Errorf("resolveMaxRetries(%d)=(%d,%v), want (0,nil)", value, got, err)
		}
	}
}

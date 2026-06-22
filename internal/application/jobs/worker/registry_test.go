// Package worker — Registry API contract tests.
//
// These tests pin the invariants called out in
// docs/worker/W1_REMOTE_WORKER_REGISTRY.md §Phase 1 — Strengthen the registry API.
// They are kept in the same package so they can access the unexported sentinel
// errors directly without exporting fiction.
package worker

import (
	"context"
	"errors"
	"sync"
	"testing"

	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// stubHandler is a no-op handler used by every positive-path test.
func stubHandler(_ context.Context, _ *domainjob.Job, _ *Tools) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

// ── Registration invariants ─────────────────────────────────────────────

func TestRegistry_RegisterValid(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("test.job", stubHandler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len: expected 1, got %d", r.Len())
	}
	if !r.Has("test.job") {
		t.Fatal(`Has("test.job"): expected true`)
	}
}

func TestRegistry_RejectsEmptyJobType(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", stubHandler); !errors.Is(err, ErrEmptyJobType) {
		t.Fatalf("expected ErrEmptyJobType, got %v", err)
	}
}

func TestRegistry_RejectsWhitespaceJobType(t *testing.T) {
	r := NewRegistry()
	for _, c := range []string{"   ", "\t", "\n", " \t\n "} {
		if err := r.Register(c, stubHandler); !errors.Is(err, ErrEmptyJobType) {
			t.Fatalf("whitespace %q: expected ErrEmptyJobType, got %v", c, err)
		}
	}
}

func TestRegistry_RejectsNilHandler(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("test.job", nil); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("expected ErrNilHandler, got %v", err)
	}
}

func TestRegistry_RejectsDuplicateRegistration(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("test.job", stubHandler); err != nil {
		t.Fatalf("first Register returned %v", err)
	}
	if err := r.Register("test.job", stubHandler); !errors.Is(err, ErrDuplicateHandler) {
		t.Fatalf("duplicate: expected ErrDuplicateHandler, got %v", err)
	}
}

func TestRegistry_RejectsRegistrationAfterFreeze(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("a", stubHandler)
	r.Freeze()
	if err := r.Register("b", stubHandler); !errors.Is(err, ErrRegistryFrozen) {
		t.Fatalf("expected ErrRegistryFrozen, got %v", err)
	}
}

// ── JobTypes() contract ─────────────────────────────────────────────────

func TestRegistry_JobTypesSortedAndDeterministic(t *testing.T) {
	r := NewRegistry()
	for _, jt := range []string{"z", "a", "m", "b"} {
		if err := r.Register(jt, stubHandler); err != nil {
			t.Fatalf("Register(%q) returned %v", jt, err)
		}
	}
	want := []string{"a", "b", "m", "z"}
	got := r.JobTypes()
	if !equalStrings(got, want) {
		t.Fatalf("sorted: want %v, got %v", want, got)
	}
	again := r.JobTypes()
	if !equalStrings(got, again) {
		t.Fatalf("non-deterministic: first %v again %v", got, again)
	}
}

func TestRegistry_JobTypesReturnsDefensiveCopy(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("a", stubHandler)
	types := r.JobTypes()
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	types[0] = "MUTATED"
	if r.JobTypes()[0] != "a" {
		t.Fatal("JobTypes mutation leaked into the registry; defensive copy violated")
	}
}

// ── Dispatch contract ───────────────────────────────────────────────────

func TestRegistry_DispatchSupportedType(t *testing.T) {
	r := NewRegistry()
	called := false
	h := func(_ context.Context, _ *domainjob.Job, _ *Tools) (map[string]any, error) {
		called = true
		return map[string]any{"ran": true}, nil
	}
	if err := r.Register("test.job", h); err != nil {
		t.Fatalf("Register returned %v", err)
	}
	res, err := r.Dispatch(context.Background(), &domainjob.Job{Type: "test.job"}, &Tools{})
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if !called {
		t.Fatal("handler was not invoked")
	}
	if res["ran"] != true {
		t.Fatalf("expected ran=true, got %v", res)
	}
}

func TestRegistry_DispatchUnsupportedTypeReturnsSentinel(t *testing.T) {
	r := NewRegistry()
	_, err := r.Dispatch(context.Background(), &domainjob.Job{Type: "missing.type"}, &Tools{})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !errors.Is(err, ErrHandlerNotRegistered) {
		t.Fatalf("expected ErrHandlerNotRegistered in error chain, got %v", err)
	}
}

// ── Concurrent reads ─────────────────────────────────────────────────────

func TestRegistry_ConcurrentReads(t *testing.T) {
	r := NewRegistry()
	for _, jt := range []string{"a", "b", "c", "d"} {
		_ = r.Register(jt, stubHandler)
	}
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_ = r.JobTypes()
			_ = r.Has("a")
			_ = r.Len()
		}()
	}
	wg.Wait()
}

// ── helpers ──────────────────────────────────────────────────────────────

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

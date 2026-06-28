// Package usecase — memory_cache_adapter_test.go covers the nil-safety
// surface of MemoryCacheAdapter so a future drift that loses the
// nil-receiver guard is caught at unit-test go-time rather than at the
// first /api/script/generate invocation.
//
// Why we test only nil-safety:
//
// MemoryCacheAdapter wraps *adapters.Service (gemmamemory). That
// concrete requires a real *sql.DB (via adapters.NewRepository),
// so a populated-svc test needs a temp DB. The integration coverage
// already exercises the populate path via engine_test.go::Engine
// (which uses a fakeMemoryGate at the narrow interface, but the
// same shape of call signature). What the populate-path tests do
// NOT cover is the nil-receiver guard — and that is precisely
// where wiring bugs in the composition root manifest
// (`BuildAIBundle` constructs memorySvc from a non-nil DB, but
// partial test fixtures pass nil).
//
// These three tests pin the nil-safety contract:
//  1. TestMemoryCacheAdapter_NilSvc — both methods return zero
//     values + nil error (caller forgot to nil-check still gets a
//     deterministic no-op).
//  2. TestMemoryCacheAdapter_NilAdapter — method calls on a nil
//     pointer do NOT panic (Go semantics: typed-nil methods that
//     don't dereference the receiver are safe).
//  3. TestMemoryCacheAdapter_CompileTimeAssertion — pins the
//     canonical/narrow vs adapter/local-types contract at
//     compile time, surfaced if someone splits the interface.
package usecase

import (
	"context"
	"testing"
)

// TestMemoryCacheAdapter_NilSvc: nil-wrapped service means both
// methods are no-ops. Callers that forget to nil-check still get
// deterministic zero values rather than a panic.
func TestMemoryCacheAdapter_NilSvc(t *testing.T) {
	adapter := NewMemoryCacheAdapter(nil)

	// CheckGate: cache-miss signal (nil res, nil err).
	res, err := adapter.CheckGate(context.Background(), memoryGateRequest{
		ChannelID: "ch-1",
		Title:     "test title",
		Prompt:    "test prompt",
	})
	if err != nil {
		t.Fatalf("CheckGate(nil-svc): unexpected err=%v", err)
	}
	if res != nil {
		t.Fatalf("CheckGate(nil-svc): expected nil result, got %#v", res)
	}

	// EvictExactOutputs: zero count, no error.
	n, err := adapter.EvictExactOutputs(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EvictExactOutputs(nil-svc): unexpected err=%v", err)
	}
	if n != 0 {
		t.Fatalf("EvictExactOutputs(nil-svc): expected count=0, got %d", n)
	}
}

// TestMemoryCacheAdapter_NilAdapter: method calls on a nil pointer do
// NOT panic. The implementation guards with `a == nil || a.svc ==
// nil` at the top of each method, so the Go-typed-nil semantics are
// preserved end-to-end (a nil pointer with no field dereference at
// the top is safe).
func TestMemoryCacheAdapter_NilAdapter(t *testing.T) {
	var adapter *MemoryCacheAdapter // nil

	res, err := adapter.CheckGate(context.Background(), memoryGateRequest{})
	if err != nil {
		t.Fatalf("CheckGate(nil-adapter): unexpected err=%v", err)
	}
	if res != nil {
		t.Fatalf("CheckGate(nil-adapter): expected nil result, got %#v", res)
	}

	n, err := adapter.EvictExactOutputs(context.Background(), nil)
	if err != nil {
		t.Fatalf("EvictExactOutputs(nil-adapter): unexpected err=%v", err)
	}
	if n != 0 {
		t.Fatalf("EvictExactOutputs(nil-adapter): expected count=0, got %d", n)
	}
}

// (Note: the compile-time assertion `var _ memoryCache = (*MemoryCacheAdapter)(nil)`
// lives at the bottom of memory_cache_adapter.go in the production
// source — there is no test-side duplicate. AGENTS.md "no dead code": a
// redundant package-level assertion in a test function body would be
// cosmetic-only and adds zero coverage — the production assertion pins
// the contract at compile time already.)

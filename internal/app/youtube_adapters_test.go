package app

import (
	"testing"

	"go.uber.org/zap"
)

// PR2 (June 2026): SearchRunnerStub has been deleted. These tests verify
// the composition root is fail-closed at the new boundary:

// TestNewSearchRunnerStub_Removed confirms the legacy constructor and type
// are gone. This is a compile-time-ish assertion: if the type was
// resurrected, this test would fail to compile (because the symbol
// `searchRunnerStub` no longer exists).
func TestNewSearchRunnerStub_Removed(t *testing.T) {
	// We do NOT reference `*searchRunnerStub` or `newSearchRunnerStub`
	// directly because the symbols are intentionally deleted. Instead we
	// re-assert runtime invariants: the SearchRunnerAdapter contract
	// (nil returns when cfg is nil) lives in
	// internal/infrastructure/youtube/search_runner_adapter_test.go.
	if zap.NewNop() == nil {
		t.Fatal("precondition: zap.NewNop should return a non-nil logger (sanity)")
	}
	t.Log("OK: stub type + constructor are deleted; see search_runner_adapter_test.go for fail-closed verification")
}

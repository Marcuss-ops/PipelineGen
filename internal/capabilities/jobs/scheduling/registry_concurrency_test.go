package scheduling

import "testing"

// TestScriptGenerateRunsTwoLeasesPerWorker is ELIM-04: script.generate is
// I/O-bound, so one lease per worker left a measured p90 queue wait of 24 s
// (max 809 s) while the worker sat idle waiting on providers.
func TestScriptGenerateRunsTwoLeasesPerWorker(t *testing.T) {
	registry := Compose()
	if got := registry.Concurrency(TypeScriptGenerate); got != DefaultScriptGenerateConcurrency {
		t.Fatalf("script.generate concurrency = %d, want %d", got, DefaultScriptGenerateConcurrency)
	}
	if DefaultScriptGenerateConcurrency != 2 {
		t.Fatalf("DefaultScriptGenerateConcurrency = %d, want 2 (the measured burst budget)", DefaultScriptGenerateConcurrency)
	}
}

// TestClipRenderSubmitRunsTwoLeases: the submit half is local prepararation
// and does not own the GPU lane; the settle half is on its own dedicated pool.
// Two submit leases are the conservative measured starting point (CR-13).
func TestClipRenderSubmitRunsTwoLeases(t *testing.T) {
	registry := Compose()
	if got := registry.Concurrency(TypeClipRender); got != 2 {
		t.Fatalf("clip.render submit concurrency = %d, want 2", got)
	}
}

// TestUnregisteredTypesKeepTheHistoricalDefault keeps the change scoped: the
// global default stays 1, so a job type that did not opt in is unaffected.
func TestUnregisteredTypesKeepTheHistoricalDefault(t *testing.T) {
	registry := Compose()
	if got := registry.Concurrency("no.such.job"); got != DefaultConcurrency {
		t.Fatalf("unregistered concurrency = %d, want %d", got, DefaultConcurrency)
	}
}

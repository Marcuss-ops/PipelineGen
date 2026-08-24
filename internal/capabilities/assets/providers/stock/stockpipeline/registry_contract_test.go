// Package stockpipeline — registry_contract_test.go.
//
// INVERSE TDD contract test for `media.stock` registry entry. The stock
// pipeline is the ONE job type in the PR-COMPLETE-WORKER-BROAD-FIX wave
// that does NOT get the ProducesArtifacts flip — it uses the
// JobFinalizer.CompleteWithArtifacts SPINE as its terminal-flip + artifact-
// write seam, not a per-item tx like voiceover/YouTube.
//
// Mirrors the voiceover batch + promo test pattern at
// `internal/application/voiceover/jobs/registry_contract_test.go`
// (PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH + PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-PROMO,
// 2026-07-04), but with INVERSE assertions: ProducesArtifacts=true
// (NOT false), ProducesArtifactsMap INCLUDES (NOT excludes).
//
// godlike/06 SSOT (one canonical owner per fact): the registry contract
// lives in `internal/capabilities/jobs/queue/registry.go`; the spine surface
// (finalizer.CompleteWithArtifacts + 6-step orchestrator + *RunSummary
// envelope + 4 typed sentinels) lives in `upload_orchestration.go` +
// `orchestrator_steps.go`; the per-orchestrator wiring is in
// `service.go::runOrchestratorResilient` (calls
// `Orchestrator.WithJobFinalizer(s.finalizer)` before invoking
// `RunResilient`).
//
// godlike/07 no-fake-availability: this test file is the load-bearing
// audit-pin. The 2 INVERSE assertions (ProducesArtifacts=true + map
// includes media.stock) LOCK the verified-canonical-spine-surface
// contract — a future refactor that silently flips ProducesArtifacts
// (or removes the jobFinalizer field) would surface as a test failure
// or build failure, not a runtime SSOT drift.
//
// godlike/07 minimum-blast-radius: pure documentation pin — no new
// surface contracts, no new dependencies, no new infrastructure. The
// 2 compile-time pins use the SAFE struct-literal + method-value
// pattern (NOT `(*Orchestrator)(nil).jobFinalizer` which would
// PANIC at package init time on Go's nil-pointer-dereference rule).
//
// Honest scope-lock (godlike/07): the runtime test surface
// (run_upload_indexing_test.go) already covers the orchestrator's
// spine behavior at runtime; this contract test is COMPLEMENTARY
// (locks the registry contract + the struct field + the method
// existence, while the existing tests lock the orchestrator behavior).
package stockpipeline

import (
	"strings"
	"testing"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// SAFE compile-time pins (no nil-deref panic at package init).
//
//  1. Struct-literal pattern: forces the `jobFinalizer` field to exist
//     on the Orchestrator struct. The struct is allocated (not
//     dereferenced via nil), so this is safe at package init.
//
//  2. Method-value pattern: forces the `WithJobFinalizer` method to
//     exist on *Orchestrator with the canonical signature. The method
//     value is constructed (not called), so this is safe at package
//     init.
//
// If a future refactor removes either the field or the method, this
// file fails to COMPILE — surfacing the SSOT drift as a build failure
// rather than a runtime panic.
var (
	_ = Orchestrator{jobFinalizer: nil}  // field existence pin
	_ = (*Orchestrator).WithJobFinalizer // method existence pin
)

// TestMediaStock_KeepsProducesArtifactsTrue_ForSpineFlow is the
// INVERSE of the voiceover batch + promo tests (which assert
// ProducesArtifacts=false + map excludes). The stock pipeline
// must RETAIN ProducesArtifacts=true because the worker calls
// *finalizer.CompleteWithArtifacts directly, NOT the legacy
// SQLiteStore.Complete path.
func TestMediaStock_KeepsProducesArtifactsTrue_ForSpineFlow(t *testing.T) {
	reg := appjobs.Compose()
	if reg == nil {
		t.Fatal("appjobs.Compose() returned nil registry")
	}
	if !reg.IsRegistered(appjobs.TypeMediaStock) {
		t.Fatalf("job type %q must be registered (Compose missing this entry)", appjobs.TypeMediaStock)
	}
	entry, _ := reg.Get(appjobs.TypeMediaStock)

	// INVERSE of the voiceover test: ProducesArtifacts must be TRUE.
	if !reg.ProducesArtifacts(appjobs.TypeMediaStock) {
		t.Fatalf("registry.ProducesArtifacts(%q) = false; want true (spine flow requires it; a false value would re-route the broker through the legacy Complete path which the SQL-layer ErrCompleteJobPathViolation guard at repository_lifecycle.go:108-115 REJECTS, breaking every stock job)", appjobs.TypeMediaStock)
	}
	if got, want := reg.Timeout(appjobs.TypeMediaStock), 60*time.Minute; got != want {
		t.Fatalf("registry.Timeout(%q) = %s; want %s", appjobs.TypeMediaStock, got, want)
	}
	if got, want := reg.DefaultMaxRetries(appjobs.TypeMediaStock), 1; got != want {
		t.Fatalf("registry.DefaultMaxRetries(%q) = %d; want %d", appjobs.TypeMediaStock, got, want)
	}

	// Audit-pin: the Description string must name the canonical
	// spine seam. A refactor that changes the spine call site or
	// the orchestrator entry point MUST update this Description
	// AND this test (godlike/06 3-surface lockstep with
	// CHANGELOG.md + AGENTS.md).
	if got := entry.Description; !strings.Contains(got, "JobFinalizer.CompleteWithArtifacts SPINE inside Service.runOrchestratorResilient") {
		t.Errorf("registry drift: TypeMediaStock.Description missing audit-pin substring \"JobFinalizer.CompleteWithArtifacts SPINE inside Service.runOrchestratorResilient\"\n  got: %s", got)
	}
	if got := entry.Description; !strings.Contains(got, "the spine call is the terminal-flip + artifact-write seam") {
		t.Errorf("registry drift: TypeMediaStock.Description missing spine-semantics substring \"the spine call is the terminal-flip + artifact-write seam\"\n  got: %s", got)
	}
}

// TestMediaStock_IsInProducesArtifactsMap_ForSpineFlow asserts that
// the ProducesArtifactsMap (the SQL-layer gate's lookup) INCLUDES
// media.stock. This is the INVERSE of the voiceover test (which
// asserts the map EXCLUDES voiceover batch + promo). The map
// inclusion is what triggers the `ErrCompleteJobPathViolation`
// guard at repository_lifecycle.go:108-115 — the guard CORRECTLY
// REJECTS the legacy Complete call for media.stock, forcing the
// worker to use the spine path.
func TestMediaStock_IsInProducesArtifactsMap_ForSpineFlow(t *testing.T) {
	reg := appjobs.Compose()
	if reg == nil {
		t.Fatal("appjobs.Compose() returned nil registry")
	}
	m := reg.ProducesArtifactsMap()
	if m == nil {
		t.Fatal("ProducesArtifactsMap() returned nil map")
	}
	// INVERSE of the voiceover test: media.stock MUST be in the map.
	if !m[appjobs.TypeMediaStock] {
		t.Fatalf("ProducesArtifactsMap() excludes %q; want present (the SQL-layer gate would let the legacy Complete path through, bypassing the spine and breaking every stock job)", appjobs.TypeMediaStock)
	}
}

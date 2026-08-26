// Package jobs (test) — registry_contract_test.go.
//
// TDD coverage for the PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH +
// PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-PROMO (July 2026) closure: the
// canonical registry entries for voiceover.batch + voiceover.promo MUST
// have ProducesArtifacts=false so the broker's mark-SUCCEEDED path routes
// through the legacy SQLiteStore.Complete (the canonical terminal-flip
// seam for jobs whose worker persists its own artifacts inside the
// caller's tx).
//
// Mirrors the YouTube fix (PR-COMPLETE-WORKER-YT-FIX, commit b8c96035,
// 2026-07-04) and the voiceover.generate fix (PR-VO-COMPLETEPATH-FIX,
// commit db2f3b1e, 2026-07-04) — the third + fourth in the family of
// per-job-type registry flips documented in
// architecture/current.yaml#PR-COMPLETE-WORKER-BROAD-FIX.
//
// The voiceover batch + promo pipelines persist their own artifacts
// (voiceovers row + media_assets projection + asset.index outbox event
// + voiceover.cleanup outbox event) atomically inside the per-item
// caller-owned tx through VoiceoverFinalizer.Finalize
// (internal/application/voiceover/finalizer.go) — distinct from the
// JobFinalizer.CompleteWithArtifacts spine that script.generate uses.
// Marking ProducesArtifacts=false re-routes the broker's "mark
// SUCCEEDED" path through the legacy SQLiteStore.Complete which is the
// CANONICAL terminal-flip seam for these job types today.
//
// godlike/07 fail-closed: a future contributor who flips
// ProducesArtifacts=true on these entries without first wiring the
// caller-owned tx for artifact persistence will re-introduce the
// SQL-layer ErrCompleteJobPathViolation gate at
// internal/platform/sqlite/jobs/repository_lifecycle.go:108-115
// and the job will be marked FAILED with the canonical "legacy
// Complete path is forbidden for artifact-producing jobs" diagnostic.
package jobs

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// TestVoiceoverBatch_RoutesToLegacyComplete pins the registry contract
// for the canonical voiceover batch job type: ProducesArtifacts=false,
// Timeout=30min, DefaultMaxRetries=2, Description=canonical audit-pin
// string.
//
// The ProducesArtifacts=false invariant is the load-bearing assertion
// (a future flip would re-introduce the SQL-layer gate and the
// voiceover E2E test would fail with "legacy Worker cannot complete
// artifact-producing job").
//
// The Description string is INTENTIONALLY pinned to the byte sequence
// below. The wording is part of the audit trail (mirrors the voiceover
// db2f3b1e + YouTube b8c96035 comment blocks); a future contributor
// who edits it for grammar must update this test in lockstep —
// otherwise the audit-pin drifts from the docstring.
func TestVoiceoverBatch_RoutesToLegacyComplete(t *testing.T) {
	const wantDescription = "Voiceover batch generation (per-item artifacts persisted inside the per-item caller-owned tx via Service.GenerateBatch → finalizeStage → voiceover.Finalizer.Finalize → tx.Commit; broker's legacy Complete is the canonical mark-SUCCEEDED seam)"

	reg := appjobs.Compose()
	if reg == nil {
		t.Fatal("appjobs.Compose() returned nil registry")
	}
	if !reg.IsRegistered(appjobs.TypeVoiceoverBatch) {
		t.Fatalf("job type %q must be registered (Compose missing this entry)", appjobs.TypeVoiceoverBatch)
	}
	entry, _ := reg.Get(appjobs.TypeVoiceoverBatch)
	if reg.ProducesArtifacts(appjobs.TypeVoiceoverBatch) {
		t.Fatalf("registry.ProducesArtifacts(%q) = true; want false (PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH mirrors the voiceover db2f3b1e + YouTube b8c96035 fixes; a true value would re-trigger the SQL-layer ErrCompleteJobPathViolation guard at repository_lifecycle.go:108-115)", appjobs.TypeVoiceoverBatch)
	}
	if got, want := reg.Timeout(appjobs.TypeVoiceoverBatch), 30*time.Minute; got != want {
		t.Fatalf("registry.Timeout(%q) = %s; want %s", appjobs.TypeVoiceoverBatch, got, want)
	}
	if got, want := reg.DefaultMaxRetries(appjobs.TypeVoiceoverBatch), 2; got != want {
		t.Fatalf("registry.DefaultMaxRetries(%q) = %d; want %d", appjobs.TypeVoiceoverBatch, got, want)
	}
	if got := entry.Description; got != wantDescription {
		t.Fatalf("Description drifted (audit-pin):\n  got:  %q\n  want: %q\nIf the wording change is intentional, update this test in lockstep.", got, wantDescription)
	}
}

// TestVoiceoverBatch_NotInProducesArtifactsMap is a secondary pin:
// ProducesArtifactsMap() is the read-only map the SQLiteStore gate
// consumes (internal/platform/sqlite/jobs). Confirming
// the voiceover batch entry is absent locks the gate's view of the
// world to the same value as the typed accessor — a divergence
// between the two would silently let the gate re-allow the legacy
// path for this job type.
func TestVoiceoverBatch_NotInProducesArtifactsMap(t *testing.T) {
	reg := appjobs.Compose()
	m := reg.ProducesArtifactsMap()
	if m == nil {
		t.Fatal("ProducesArtifactsMap() returned nil map")
	}
	if m[appjobs.TypeVoiceoverBatch] {
		t.Fatalf("ProducesArtifactsMap() includes %q; want absent (PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-BATCH)", appjobs.TypeVoiceoverBatch)
	}
}

// TestVoiceoverPromo_RoutesToLegacyComplete pins the registry contract
// for the canonical voiceover promo job type. Mirrors
// TestVoiceoverBatch_RoutesToLegacyComplete (same invariant, same
// rationale) with a distinct Description audit-pin string for the
// promo pipeline.
//
// The promo pipeline's per-item path goes through:
//
//	Service.GeneratePromo → promo.NewGenerator → promoVoiceoverAdapter →
//	ProcessVoiceoverItemUseCase.Execute → ProcessSegmentUseCase.Execute →
//	voiceover.Finalizer.Finalize → tx.Commit
//
// (See internal/application/voiceover/promo.go + service.go. P0-#3
// cutover July 2026: the legacy voiceoverGenBridge → Service.
// GenerateWithDestination route was replaced by promoVoiceoverAdapter
// → ProcessVoiceoverItemUseCase.Execute, the SAME per-item use case
// the voiceover.generate + voiceover.generate_item job paths use.
// The artifact-persistence contract is therefore IDENTICAL to the
// batch pipeline: voiceovers + media_assets + outbox events written
// INSIDE the caller's tx BEFORE the broker's mark-SUCCEEDED call.
func TestVoiceoverPromo_RoutesToLegacyComplete(t *testing.T) {
	const wantDescription = "Voiceover promo generation (translate + generate) (per-item artifacts persisted inside the per-item caller-owned tx via Service.GeneratePromo → promo.NewGenerator → promoVoiceoverAdapter → ProcessVoiceoverItemUseCase.Execute → ProcessSegmentUseCase.Execute → voiceover.Finalizer.Finalize → tx.Commit; broker's legacy Complete is the canonical mark-SUCCEEDED seam)"

	reg := appjobs.Compose()
	if reg == nil {
		t.Fatal("appjobs.Compose() returned nil registry")
	}
	if !reg.IsRegistered(appjobs.TypeVoiceoverPromo) {
		t.Fatalf("job type %q must be registered (Compose missing this entry)", appjobs.TypeVoiceoverPromo)
	}
	entry, _ := reg.Get(appjobs.TypeVoiceoverPromo)
	if reg.ProducesArtifacts(appjobs.TypeVoiceoverPromo) {
		t.Fatalf("registry.ProducesArtifacts(%q) = true; want false (PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-PROMO mirrors the voiceover db2f3b1e + YouTube b8c96035 fixes; a true value would re-trigger the SQL-layer ErrCompleteJobPathViolation guard at repository_lifecycle.go:108-115)", appjobs.TypeVoiceoverPromo)
	}
	if got, want := reg.Timeout(appjobs.TypeVoiceoverPromo), 30*time.Minute; got != want {
		t.Fatalf("registry.Timeout(%q) = %s; want %s", appjobs.TypeVoiceoverPromo, got, want)
	}
	if got, want := reg.DefaultMaxRetries(appjobs.TypeVoiceoverPromo), 2; got != want {
		t.Fatalf("registry.DefaultMaxRetries(%q) = %d; want %d", appjobs.TypeVoiceoverPromo, got, want)
	}
	if got := entry.Description; got != wantDescription {
		t.Fatalf("Description drifted (audit-pin):\n  got:  %q\n  want: %q\nIf the wording change is intentional, update this test in lockstep.", got, wantDescription)
	}
}

// TestVoiceoverPromo_NotInProducesArtifactsMap is a secondary pin
// mirroring TestVoiceoverBatch_NotInProducesArtifactsMap. A divergence
// between the typed accessor (ProducesArtifacts) and the map
// (ProducesArtifactsMap) would silently let the SQLiteStore gate
// re-allow the legacy path for this job type.
func TestVoiceoverPromo_NotInProducesArtifactsMap(t *testing.T) {
	reg := appjobs.Compose()
	m := reg.ProducesArtifactsMap()
	if m == nil {
		t.Fatal("ProducesArtifactsMap() returned nil map")
	}
	if m[appjobs.TypeVoiceoverPromo] {
		t.Fatalf("ProducesArtifactsMap() includes %q; want absent (PR-COMPLETE-WORKER-FIX-TYPE-VOICEOVER-PROMO)", appjobs.TypeVoiceoverPromo)
	}
}

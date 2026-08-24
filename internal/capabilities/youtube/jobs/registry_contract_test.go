// Package jobs (test) — registry_contract_test.go.
//
// TDD coverage for the PR-COMPLETE-WORKER-YT-FIX (July 2026)
// closure: the canonical registry entry for youtube_clip.extract
// MUST have ProducesArtifacts=false so the broker's mark-SUCCEEDED
// path routes through the legacy SQLiteStore.Complete (the canonical
// terminal-flip seam for jobs whose worker persists its own artifacts
// inside the caller's tx).
//
// Mirrors the voiceover fix (PR-VO-COMPLETEPATH-FIX, commit db2f3b1e,
// 2026-07-04) where TypeVoiceoverGenerate + TypeVoiceoverGenerateItem
// were both flipped the same way. The YouTube pipeline persists its
// own clip artifacts (media_assets rows + outbox events for
// asset.index + voiceover_cleanup) inside the per-segment
// caller-owned tx via process_segment + ClipAtomicWriter, so the
// broker doesn't need a second persistence step.
//
// godlike/07 fail-closed: a future contributor who flips
// ProducesArtifacts=true on this entry without first wiring the
// caller-owned tx for artifact persistence will re-introduce the
// SQL-layer ErrCompleteJobPathViolation gate at
// internal/platform/sqlite/jobs/repository_lifecycle.go:115
// and the job will be marked FAILED with the canonical "legacy
// Complete path is forbidden for artifact-producing jobs" diagnostic.
package jobs

import (
	"testing"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestYouTubeClipExtract_RoutesToLegacyComplete pins the registry
// contract for the canonical YouTube clip extraction job type:
// ProducesArtifacts=false, Timeout=60min, DefaultMaxRetries=2,
// Description=canonical audit-pin string.
// The ProducesArtifacts=false invariant is the load-bearing assertion
// (a future flip would re-introduce the SQL-layer gate and the
// Pacquiao-broner end-to-end test would fail with
// "legacy Worker cannot complete artifact-producing job").
//
// The Description string is INTENTIONALLY pinned to the byte sequence
// below. The wording is part of the audit trail (mirrors the
// voiceover db2f3b1e comment block); a future contributor who edits
// it for grammar must update this test in lockstep — otherwise the
// audit-pin drifts from the docstring.
func TestYouTubeClipExtract_RoutesToLegacyComplete(t *testing.T) {
	const wantDescription = "YouTube clip extraction (per-segment artifacts persisted inside the per-segment caller-owned tx via process_segment + ClipAtomicWriter; broker's legacy Complete is the canonical mark-SUCCEEDED seam)"

	reg := appjobs.Compose()
	if reg == nil {
		t.Fatal("appjobs.Compose() returned nil registry")
	}
	if !reg.IsRegistered(appjobs.TypeYouTubeClipExtract) {
		t.Fatalf("job type %q must be registered (Compose missing this entry)", appjobs.TypeYouTubeClipExtract)
	}
	entry, _ := reg.Get(appjobs.TypeYouTubeClipExtract)
	if reg.ProducesArtifacts(appjobs.TypeYouTubeClipExtract) {
		t.Fatalf("registry.ProducesArtifacts(%q) = true; want false (PR-COMPLETE-WORKER-YT-FIX mirrors the voiceover db2f3b1e fix; a true value would re-trigger the SQL-layer ErrCompleteJobPathViolation guard at repository_lifecycle.go:108-115)", appjobs.TypeYouTubeClipExtract)
	}
	if got, want := reg.Timeout(appjobs.TypeYouTubeClipExtract), 60*time.Minute; got != want {
		t.Fatalf("registry.Timeout(%q) = %s; want %s", job.TypeYouTubeClipExtract, got, want)
	}
	if got, want := reg.DefaultMaxRetries(appjobs.TypeYouTubeClipExtract), 2; got != want {
		t.Fatalf("registry.DefaultMaxRetries(%q) = %d; want %d", job.TypeYouTubeClipExtract, got, want)
	}
	if got := entry.Description; got != wantDescription {
		t.Fatalf("Description drifted (audit-pin):\n  got:  %q\n  want: %q\nIf the wording change is intentional, update this test in lockstep.", got, wantDescription)
	}
}

// TestYouTubeClipExtract_NotInProducesArtifactsMap is a secondary
// pin: ProducesArtifactsMap() is the read-only map the SQLiteStore
// gate consumes (internal/platform/sqlite/jobs).
// Confirming the YouTube entry is absent locks the gate's view of
// the world to the same value as the typed accessor — a divergence
// between the two would silently let the gate re-allow the legacy
// path for this job type.
func TestYouTubeClipExtract_NotInProducesArtifactsMap(t *testing.T) {
	reg := appjobs.Compose()
	m := reg.ProducesArtifactsMap()
	if m == nil {
		t.Fatal("ProducesArtifactsMap() returned nil map")
	}
	if m[appjobs.TypeYouTubeClipExtract] {
		t.Fatalf("ProducesArtifactsMap() includes %q; want absent (PR-COMPLETE-WORKER-YT-FIX)", appjobs.TypeYouTubeClipExtract)
	}
}

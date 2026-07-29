// Package stockpipeline — run_success_gate_test.go (Stock §12-1 P0
// #1, July 2026).
//
// TDD regression tests for AssertRunSummaryArtifactsRequired — the
// orchestrator-level fail-closed gate added in the same commit
// chain. Tests pin the verdict-spec contract: RunSummary.Manifest
// MUST declare at least one Required:true chunk AND one Required:true
// metadata.json entry before Orchestrator.Run returns nil error.
//
// godlike/06 SSOT: the cross-package caller pattern is
// errors.Is(err, stockpipeline.ErrMetadataMissing) OR
// errors.Is(err, stockpipeline.ErrNoProducedChunk). Tests use the
// direct sentinel probe (no wrap noise) to keep the assertion
// table stable across future wrap-format changes.
//
// Kind-string note: video chunks use finalization.KindVideo (= "video"),
// the same typed constant BuildFinalizationRequest uses for the chunk
// artifacts (finalizer_gates.go:363). This intentionally matches the
// post-publish gate's kind-string surface so a future operator query
// ("which artifacts are video chunks?") returns consistent results
// across both layers (orchestrator-level vs post-publish).
//
// Coverage matrix (priority order, fail-fast):
//
//	  nil RunSummary                  → ErrMetadataMissing
//	  nil Manifest                    → ErrMetadataMissing
//	  empty Manifest.Artifacts        → ErrMetadataMissing (priority)
//	  metadata Required:true, no video → ErrNoProducedChunk
//	  metadata Required:false + video → ErrMetadataMissing (priority)
//	  both Required:true              → nil (success)
//	  metadata Required:true, many
//
//		Required chunks + extras          → nil (multi-entry OK)
//
//	  (canary) buildStockManifest today (all
//
//		Required:false entries)            → ErrMetadataMissing
package stockpipeline

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestAssertRunSummaryArtifactsRequired_NilReceiver pins the nil-
// receiver defence-in-depth contract (godlike/07 no-fake-availability).
// A WireStockPipeline wiring oversight (nil summary returned by future
// refactor) MUST surface ErrMetadataMissing rather than panic, so the
// orchestrator's typed-error chain stays intact.
func TestAssertRunSummaryArtifactsRequired_NilReceiver(t *testing.T) {
	if err := AssertRunSummaryArtifactsRequired(nil); !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("nil RunSummary: want errors.Is(ErrMetadataMissing) == true, got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_NilManifest pins the
// empty-manifest defense-in-depth contract. Same panic-avoidance
// rationale as TestAssertRunSummaryArtifactsRequired_NilReceiver.
func TestAssertRunSummaryArtifactsRequired_NilManifest(t *testing.T) {
	s := &RunSummary{Manifest: nil, FinalStatus: job.StatusSucceeded}
	if err := AssertRunSummaryArtifactsRequired(s); !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("nil Manifest: want errors.Is(ErrMetadataMissing) == true, got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_EmptyArtifacts pins the empty
// manifest's fail-closed behaviour. Pre-commit-4-7 buildStockManifest
// emits all entries with Required:false (today's Commit 5 holdover),
// so the gate fires ErrMetadataMissing on every production stock run.
// This test owns that contract.
func TestAssertRunSummaryArtifactsRequired_EmptyArtifacts(t *testing.T) {
	s := &RunSummary{
		Manifest:    &job.ArtifactManifest{Artifacts: nil},
		FinalStatus: job.StatusSucceeded,
	}
	err := AssertRunSummaryArtifactsRequired(s)
	if !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("empty artifacts: want ErrMetadataMissing, got %v", err)
	}
	if errors.Is(err, ErrNoProducedChunk) {
		t.Fatalf("empty artifacts: priority violated — should fire ErrMetadataMissing FIRST, not ErrNoProducedChunk; got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_MetadataOnly pins the priority
// ordering: when ONLY the metadata entry is Required:true (no chunk
// Required:true), the gate fires ErrNoProducedChunk. This is the
// case where the orchestrator declared the metadata envelope but
// failed to declare any chunk — the run cannot SUCCEED.
func TestAssertRunSummaryArtifactsRequired_MetadataOnly(t *testing.T) {
	s := &RunSummary{
		Manifest: &job.ArtifactManifest{
			Artifacts: []job.Artifact{
				{
					ID: StockArtifactIdMetadata, Kind: job.ArtifactKindMetadata,
					Filename: "metadata.json", MIMEType: "application/json",
					Required: true,
				},
				{
					ID: StockArtifactIdThumbnail, Kind: job.ArtifactKindImage,
					Filename: "thumbnail.png", MIMEType: "image/png",
					Required: false, // not yet hydrated, post-Commit-4-7
				},
			},
		},
		FinalStatus: job.StatusSucceeded,
	}
	err := AssertRunSummaryArtifactsRequired(s)
	if !errors.Is(err, ErrNoProducedChunk) {
		t.Fatalf("metadata-only (no chunk Required): want ErrNoProducedChunk, got %v", err)
	}
	if errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("metadata-only: gate should have PASSED metadata check and FAILED chunk check, not double-fire; got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_ChunkOnly pins the priority
// ordering vice-versa: when ONLY the chunk entry is Required:true
// (no metadata Required:true), the gate fires ErrMetadataMissing
// FIRST. This is the case where the chunk is declared but the
// metadata envelope is missing — the run cannot SUCCEED because a
// future operator cannot reconstruct the chunk without metadata.
func TestAssertRunSummaryArtifactsRequired_ChunkOnly(t *testing.T) {
	s := &RunSummary{
		Manifest: &job.ArtifactManifest{
			Artifacts: []job.Artifact{
				{
					ID: StockArtifactIdMetadata, Kind: job.ArtifactKindMetadata,
					Filename: "metadata.json", MIMEType: "application/json",
					Required: false, // operator forgot to flip to true
				},
				{
					ID: StockArtifactIdBindings, Kind: job.ArtifactKindClipBindings,
					Filename: "bindings.json", MIMEType: "application/json",
					Required: false,
				},
				{
					ID:       "stock:chunk:0",
					Kind:     string(finalization.KindVideo),
					Filename: "stock_chunk_0.mp4", MIMEType: "video/mp4",
					Required: true,
				},
			},
		},
		FinalStatus: job.StatusSucceeded,
	}
	err := AssertRunSummaryArtifactsRequired(s)
	if !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("chunk-only (no metadata Required): want ErrMetadataMissing, got %v", err)
	}
	if errors.Is(err, ErrNoProducedChunk) {
		t.Fatalf("chunk-only: priority violated — should fire ErrMetadataMissing FIRST, not ErrNoProducedChunk; got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_HappyPath pins the success
// contract: when both metadata Required:true AND chunk Required:true
// are present, the gate returns nil. This is the post-Commit-4-7
// production shape.
func TestAssertRunSummaryArtifactsRequired_HappyPath(t *testing.T) {
	s := &RunSummary{
		Manifest: &job.ArtifactManifest{
			Artifacts: []job.Artifact{
				{
					ID: StockArtifactIdMetadata, Kind: job.ArtifactKindMetadata,
					Filename: "metadata.json", MIMEType: "application/json",
					Path:     "/var/lib/pipelinegen/metadata.json",
					Required: true,
				},
				{
					ID:       "stock:chunk:0",
					Kind:     string(finalization.KindVideo),
					Filename: "stock_chunk_0.mp4", MIMEType: "video/mp4",
					Path:     "/var/lib/pipelinegen/chunk_0.mp4",
					Required: true,
				},
			},
		},
		FinalStatus: job.StatusSucceeded,
	}
	if err := AssertRunSummaryArtifactsRequired(s); err != nil {
		t.Fatalf("happy path: want nil, got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_MultipleChunksAndExtras pins
// the "many entries" shape: one Required metadata entry + multiple
// Required chunks + several non-Required support entries. The gate
// returns nil regardless of entry count, AS LONG AS ≥1 metadata
// Required AND ≥1 chunk Required are present.
func TestAssertRunSummaryArtifactsRequired_MultipleChunksAndExtras(t *testing.T) {
	s := &RunSummary{
		Manifest: &job.ArtifactManifest{
			Artifacts: []job.Artifact{
				{
					ID: StockArtifactIdMetadata, Kind: job.ArtifactKindMetadata,
					Filename: "metadata.json", MIMEType: "application/json",
					Required: true,
				},
				{ID: StockArtifactIdThumbnail, Kind: job.ArtifactKindImage, Filename: "thumbnail.png", Required: false},
				{ID: StockArtifactIdBindings, Kind: job.ArtifactKindClipBindings, Filename: "bindings.json", Required: false},
				{ID: StockArtifactIdReport, Kind: job.ArtifactKindScriptJSON, Filename: "report.json", Required: false},
				{ID: StockArtifactIdSummary, Kind: job.ArtifactKindScriptText, Filename: "summary.txt", Required: false},
				{ID: "stock:chunk:0", Kind: string(finalization.KindVideo), Filename: "stock_chunk_0.mp4", Required: true},
				{ID: "stock:chunk:1", Kind: string(finalization.KindVideo), Filename: "stock_chunk_1.mp4", Required: true},
				{ID: "stock:chunk:2", Kind: string(finalization.KindVideo), Filename: "stock_chunk_2.mp4", Required: true},
			},
		},
		FinalStatus: job.StatusSucceeded,
	}
	if err := AssertRunSummaryArtifactsRequired(s); err != nil {
		t.Fatalf("multi-chunk: want nil, got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_RealBuildStockManifest pins
// the production-shape contract: invoking the canonical
// buildStockManifest helper (today's Commit 5 holdover) returns a
// manifest where all 5 entries have Required:false. The gate fires
// ErrMetadataMissing (NOT ErrNoProducedChunk; priority ordering), so
// the §12-1 P0 #1 fail-closed class is closed end-to-end.
//
// This test also acts as the canary for the post-Commit-4-7 era:
// once buildStockManifest flips Required:true once LocalPath is
// hydrated, this test flips to asserting the gate returns nil.
// Counterpart flip-point: when the buildStockManifest becomes
// Required:true on metadata + ≥1 chunk, this test must be updated
// to expect nil.
func TestAssertRunSummaryArtifactsRequired_RealBuildStockManifest(t *testing.T) {
	mfst := buildStockManifest("wf-test", "job-test")
	s := &RunSummary{Manifest: mfst, FinalStatus: job.StatusSucceeded}
	err := AssertRunSummaryArtifactsRequired(s)
	// Today (Commit 5): buildStockManifest emits 5 entries with
	// Required:false — the gate MUST fire ErrMetadataMissing.
	// Post-Commit-4-7 the test will be updated to expect nil.
	if !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("buildStockManifest (Commit 5 holdover: all Required:false): want ErrMetadataMissing, got %v", err)
	}
}

// TestAssertRunSummaryArtifactsRequired_RequiredFalseSkipped pins
// the "Required:false entries are skipped by the gate" contract:
// Required:false artifacts (the post-Commit-4-7 hydration-skeleton
// placeholder entries) MUST NOT count toward hasMetadata/hasChunk
// even when the Kind matches. Today, buildStockManifest emits all
// Required:false — this test is a micro-canary confirming the
// `if !a.Required { continue }` line behaves correctly.
func TestAssertRunSummaryArtifactsRequired_RequiredFalseSkipped(t *testing.T) {
	s := &RunSummary{
		Manifest: &job.ArtifactManifest{
			Artifacts: []job.Artifact{
				{
					ID: StockArtifactIdMetadata, Kind: job.ArtifactKindMetadata,
					Filename: "metadata.json", MIMEType: "application/json",
					Required: false, // even if Kind=Metadata, this is skipped
				},
				{
					ID: "stock:chunk:0", Kind: string(finalization.KindVideo),
					Filename: "stock_chunk_0.mp4", MIMEType: "video/mp4",
					Required: false, // even if Kind=Video, this is skipped
				},
			},
		},
		FinalStatus: job.StatusSucceeded,
	}
	err := AssertRunSummaryArtifactsRequired(s)
	// Both Required:false ⇒ both skipped ⇒ neither hasMetadata nor
	// hasChunk true. Priority fires ErrMetadataMissing first.
	if !errors.Is(err, ErrMetadataMissing) {
		t.Fatalf("all Required:false (same Kind as Required entries): want ErrMetadataMissing, got %v", err)
	}
}

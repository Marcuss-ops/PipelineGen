// Package stockpipeline — stock_stager_wiring_test.go (PR-STOCK-SOURCESTAGER-WIRE, July 2026).
//
// DEPRECATED CONTRACT PIN (godlike/07 honest-limitation):
// The 5 t.Skip()-gated tests below pin the OLD contract (graceful
// degradation returning nil on all-failure). They DO NOT pin the
// current production contract — that contract changed in
// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (2026-07-04, commit pending
// push to origin/main) which adds fail-closed typed sentinels
// (ErrStockStageSourcesAllFailed + ErrStockComposeChunksAllFailed)
// at the end of StockStageSourcesStep.Run and StockComposeChunksStep.Run.
//
// The NEW contract is pinned by the 5 ACTIVE tests in
// stock_fake_availability_test.go (same package). Future
// maintainers who unskip the tests below WILL see them fail under
// the new contract — that is the expected behavior. The tests are
// retained as audit-pins for the deferred Commit 6 (STOCK-CUT-6
// forward-pointer in architecture/current.yaml) which will
// reconcile the two contracts.
//
// Forward-coverage marker for the canonical `assets.SourceStager` integration
// into Orchestrator.RunResilient dispatchSteps["stock.stage_sources"]. Mirrors
// the established pattern of:
//
//   - YouTube (process_segment.go::Step 4a — commit 93d92b78 pre-step
//     StageSource + deferred Cleanup + graceful degradation on stage
//     failure).
//   - Artlist (run_orchestrator_stages.go::stageProcessBatch — commit
//     2bc503df per-clip StageSource + per-clip Cleanup defer + graceful
//     degradation).
//
// STATUS (July 2026): the 5 test functions below are t.Skip()-gated pending
// Stock Cutover §12-4 Commit 6 (STOCK-CUT-6 forward-pointer; tracked at
// `architecture/current.yaml#PR-STOCK-SOURCESTAGER-WIRE`). Origin/main's
// RunResilient iterates dispatchSteps which currently implement
// stock.stage_sources as Begin/Complete-only — the actual
// o.stager.StageSource invocation is deferred to Commit 6 per the
// doc-comment at orchestrator.go::RunResilient (Step 2: "future Commit 6
// wires real Stage").
//
// WHEN COMMIT 6 LANDS: removing the t.Skip() blocks re-activates the 5
// contract assertions (no test body changes required; the
// newWiringTestOrchestrator helper signature follows origin/main's 6-arg
// NewTestStockOrchestrator — see FRAGILITY NOTE in the helper doc-comment).
//
// Without these tests, Commit 6's wire-up is correctness-invariant but
// not test-pinned: a future agent that breaks Step 3's invocation order,
// the SourceRef shape, the deferred Cleanup, or the Warn + continue
// graceful-degradation path will not catch the regression at unit-test
// time. The five test functions below pin:
//
//	(1) StageSource is invoked exactly once per Orchestrator.RunResilient
//	    with the firstSource URL extracted from input.DirectURLs[0].
//	(2) Cleanup is deferred and fires after RunResilient returns; the
//	    same *assets.StagedAsset instance is passed to Cleanup that was
//	    returned from StageSource (i.e. the orchestrator does not
//	    accidentally swap the asset).
//	(3) StageSource returning a non-nil error does NOT abort the run —
//	    RunResilient returns nil error + valid RunSummary with manifest
//	    still stamped (graceful degradation: stage failure → Warn +
//	    continue, mirrors YouTube + Artlist pattern).
//	(4) StageSource returning (nil, nil) — the defensive-path branch
//	    `else if staged == nil` — also gracefully degrades with no
//	    Cleanup defer.
//	(5) When firstSource yields no source (empty input), StageSource is
//	    NOT invoked; the orchestrator's plan_clips step returns the
//	    typed sentinel "no sources to plan" before stage_sources is
//	    reached. This pins the order: plan first, stage second.
package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// ─────────────────────────────────────────────────────────────────────
// recordingStager — test stub that captures StageSource + Cleanup calls.
// Compiles to assets.SourceStager via the canonical port so tests can
// stand in for StockStager without dragging in the real yt-dlp path.
// ─────────────────────────────────────────────────────────────────────

// recordingStager implements assets.SourceStager. Every call to
// StageSource / Cleanup is captured so tests can assert on the
// sequence and arguments the Orchestrator produces. The stub is
// stateful (returns stagedAsset on StageSource; nil otherwise) so
// tests can drive success / failure / nil-asset paths deterministically.
type recordingStager struct {
	stageCalls    int
	lastRef       assets.SourceRef
	stagedAsset   *assets.StagedAsset
	stageErr      error
	cleanupCalls  int
	cleanedStaged *assets.StagedAsset
	cleanupErr    error
}

// Compile-time assertion: recordingStager satisfies the canonical
// assets.SourceStager port so the orchestrator accepts it the same
// way it accepts *StockStager / *ArtlistStager / *YouTubeStager in
// production. Drift in the port signature is a build failure rather
// than a runtime panic.
var _ acquisition.SourceStager = (*recordingStager)(nil)

// Prepare records the call and returns a PrepareContext derived from the
// stubbed stagedAsset. Tests configure stagedAsset + stageErr before the run.
func (r *recordingStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	r.stageCalls++
	r.lastRef = assets.SourceRef{URL: req.Source.URL, DownloadSection: req.Source.DownloadSection, ForceKeyframes: req.Source.ForceKeyframes, MergeFormat: req.Source.MergeFormat}
	if r.stageErr != nil {
		return nil, r.stageErr
	}
	if r.stagedAsset == nil {
		return nil, nil
	}
	return &acquisition.PrepareContext{
		ID:           r.stagedAsset.SourceID,
		LocalPath:    r.stagedAsset.LocalPath,
		SizeBytes:    r.stagedAsset.Bytes,
		CleanupToken: r.stagedAsset.LocalPath,
	}, nil
}

// Release records the cleanup call.
func (r *recordingStager) Release(_ context.Context, cleanupToken string) error {
	r.cleanupCalls++
	r.cleanedStaged = &assets.StagedAsset{LocalPath: cleanupToken}
	return r.cleanupErr
}

// newWiringTestOrchestrator is a tiny constructor for tests that
// need an Orchestrator wired with a recording stager + an in-memory
// step store. Today the planner / cutter / renderer / writer /
// projection are no-ops or nil pointers (Commit 2: chunk ladder is
// stubbed via noopWriter / noopProjection / NewDeterministicPlanner;
// cutter + renderer are not invoked in Step 3's invocation path).
//
// Note on signature: this helper is calibrated to the canonical
// 5-arg NewTestStockOrchestrator (cfg, planner, stager, cutter, renderer) —
// no logger arg. Tests are t.Skip()-gated so
// they don't run against the current stub dispatchSteps[stock.stage_sources]
// (Begin/Complete only); the helper compiles cleanly against origin/main.
//
// FRAGILITY NOTE for Stock Cutover §12-4 Commit 6 (STOCK-CUT-6 forward-pointer):
// When Commit 6 extends NewOrchestrator with a new constructor
// argument, this helper signature MUST follow the new arity. The
// compile-time var _ assets.SourceStager = (*recordingStager)(nil)
// assertion below will not catch constructor arity drift; only the
// build itself does. Future maintainers: update this helper alongside
// the constructor when STOCK-CUT-6 lands.
//
// Returns the Orchestrator handle so each test can assert on stub
// state via the returned recordingStager.
func newWiringTestOrchestrator(rec *recordingStager) *Orchestrator {
	// PR-STOCK-PRODUCTION-DEPS (P2_media, July 2026): the orchestrator's
	// composition-time fail-closed gate (orchestrator.go::RunResilient)
	// rejects nil renderer with ErrOrchestratorNilDeps. Tests that
	// exercise the pipeline must wire a non-nil renderer stub even if
	// they don't assert on render behavior. The noopRenderer returns
	// success on every call so StockComposeChunksStep's happy-path
	// branch is reached (it does NOT exercise the all-failed gate;
	// that's pinned by stock_fake_availability_test.go). The
	// noopRenderer stub lives in stock_test_helpers.go per
	// godlike/06 SSOT (one canonical owner per fact).
	return NewTestStockOrchestrator(
		OrchestratorConfig{
			JobId:            "wiring-test",
			Lease:            testLease("wiring-test"),
			PolicyVersion:    "v1",
			ChunkDurationSec: 5,
			ClipDurationSec:  5,
		},
		NewDeterministicPlanner(),
		rec,
		fakeSucceedingCutter{},
		successNoopRenderer(),
	).
		WithAssetPreparation(&recordingArtifactPreparation{}).
		WithJobFinalizer(stubJobFinalizer{}).
		WithLocalFS(newRealishFakeLocalFS())
}

// ─────────────────────────────────────────────────────────────────────
// Test (1) — StageSource is invoked exactly once with firstSource URL
// ─────────────────────────────────────────────────────────────────────

// TestOrchestrator_RunResilient_StageSource_CalledOnceWithFirstSourceURL
// pins that Orchestrator.RunResilient's step 3 (stage_sources) invokes
// the canonical SourceStager port EXACTLY ONCE with the firstSource
// URL extracted from input.DirectURLs[0]. Mirrors the YouTube Step 4a
// contract that issues one StageSource per Execute call.
//
// With the recording stager + noop defaults the RunResilient path is
// deterministic (resolve_sources → plan_clips → stage_sources →
// build_manifest → emit_chunks → project_manifest all use noop or
// pass-through implementations). The test asserts `err == nil` so
// any wiring regression that introduces an error in the surrounding
// steps (e.g. accidentally wiring a valid-ProducePath but failing
// Asset.Validate) fails loud rather than passing silently.
//
// Without this test, a regression that:
//   - drops the StageSource invocation entirely (silent stage skip)
//   - invokes it N>1 times (wasted bandwidth per retry)
//   - passes a wrong URL (e.g. full search query string instead of
//     DirectURLs[0])
//
// would not be caught at unit-test time.
func TestOrchestrator_RunResilient_StageSource_CalledOnceWithFirstSourceURL(t *testing.T) {
	rec := &recordingStager{
		stagedAsset: &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", Bytes: 1024},
	}
	o := newWiringTestOrchestrator(rec)
	wantURL := "https://example.com/source.mp4"

	summary, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{wantURL},
		FolderID:      "wf-wiring-1",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err != nil {
		t.Fatalf("RunResilient returned err: %v (recordingStager + noop defaults MUST produce nil err on happy path)", err)
	}
	if summary == nil || summary.Manifest == nil {
		t.Fatal("RunResilient returned nil RunSummary.Manifest on happy path (handleStream crash or early-return bug)")
	}

	if rec.stageCalls != 1 {
		t.Errorf("StageSource called %d times, want 1 (Step 3 stage_sources must invoke the canonical SourceStager exactly once)", rec.stageCalls)
	}
	if rec.lastRef.URL != wantURL {
		t.Errorf("StageSource ref.URL = %q, want %q (orchestrator must pass firstSource URL verbatim)", rec.lastRef.URL, wantURL)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (2) — Cleanup is deferred and fires after RunResilient returns
// ─────────────────────────────────────────────────────────────────────

// TestOrchestrator_RunResilient_CleanupDeferredOnStageSuccess pins that
// the deferred Cleanup captures the SAME *assets.StagedAsset that
// StageSource returned (no copy, no nil-swap). Mirrors the YouTube
// `defer func(staged *assets.StagedAsset)` pattern at
// process_segment.go:243-249.
//
// The defer must fire AFTER RunResilient returns to the caller so the
// staged file lives until the rest of the pipeline has consumed it;
// if it fires BEFORE (e.g. inside stage_sources itself), the
// chunk-emission step would lose its staged input.
func TestOrchestrator_RunResilient_CleanupDeferredOnStageSuccess(t *testing.T) {
	rec := &recordingStager{
		stagedAsset: &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", Bytes: 4096},
	}
	o := newWiringTestOrchestrator(rec)

	summary, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/src.mp4"},
		FolderID:      "wf-cleanup-1",
		ClipDuration:  5,
		ChunkDuration: 5,
	})
	if err != nil {
		t.Fatalf("RunResilient err on happy path: %v", err)
	}
	if summary == nil || summary.Manifest == nil {
		t.Fatalf("RunResilient returned nil RunSummary.Manifest on happy path")
	}

	if rec.cleanupCalls != 1 {
		t.Errorf("Release called %d times, want 1 (defer must fire after RunResilient returns)", rec.cleanupCalls)
	}
	if rec.cleanedStaged == nil {
		t.Fatal("Release did not record a cleaned asset")
	}
	if rec.cleanedStaged.LocalPath != rec.stagedAsset.LocalPath {
		t.Errorf("Release LocalPath = %q, want %q (must match Prepare's LocalPath)", rec.cleanedStaged.LocalPath, rec.stagedAsset.LocalPath)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (3) — stage failure → graceful degradation (Warn + continue)
// ─────────────────────────────────────────────────────────────────────

// TestOrchestrator_RunResilient_StageFailure_GracefulDegradation pins
// that a non-nil StageSource error does NOT abort RunResilient. The
// orchestrator's contract (mirrors YouTube + Artlist pattern):
//
//	stager.StageSource(...) error → log Warn + continue with the
//	legacy path. Cleanup is NOT deferred when StageSource returned
//	nil or error (no staged asset to clean). The RunSummary is
//	non-nil so the caller can still surface the manifest.
//
// A regression that aborts the run on stage error would lock out
// every grain source whose yt-dlp download is temporarily flaky,
// defeating the point of having a port-mirror in production.
func TestOrchestrator_RunResilient_StageFailure_GracefulDegradation(t *testing.T) {
	t.Skip("DEPRECATED: pins the OLD graceful-degradation contract (nil err on stage failure). Superseded by stock_fake_availability_test.go::TestStockStageSourcesStep_AllSourcesFailed_ReturnsTypedError which pins the NEW fail-closed contract (ErrStockStageSourcesAllFailed). Unskip when deferred Commit 6 (PR-STOCK-SOURCESTAGER-WIRE) reconciles the contracts.")
	rec := &recordingStager{
		// Both nil: defensive pointer + intentional error together.
		stagedAsset: nil,
		stageErr:    errors.New("simulated stage failure (yt-dlp offline)"),
	}
	o := newWiringTestOrchestrator(rec)

	summary, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		FolderID:      "wf-graceful",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	// Assert 1: stage-source failure does NOT bubble up.
	if err != nil {
		t.Fatalf("RunResilient propagated stage-source error: %v (graceful degradation contract violated — caller must NOT see this)", err)
	}
	// Assert 2: RunSummary + Manifest are STILL emitted (build_manifest
	// runs AFTER stage_sources; graceful degradation on stage must
	// not skip the rest of the ladder).
	if summary == nil || summary.Manifest == nil {
		t.Fatal("RunResilient returned nil RunSummary.Manifest on stage-source failure (manifest emission skipped — regression)")
	}
	if got, want := len(summary.Manifest.Artifacts), stockArtifactCount; got != want {
		t.Errorf("manifest artifact count = %d, want %d (graceful stage failure must NOT skip build_manifest)", got, want)
	}
	// Assert 3: Cleanup is NOT deferred when StageSource returned nil.
	if rec.cleanupCalls != 0 {
		t.Errorf("Cleanup called %d times on stage failure, want 0 (nothing to clean when StageSource returned nil asset)", rec.cleanupCalls)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (4) — stage returns (nil, nil) → graceful degradation (no defer)
// ─────────────────────────────────────────────────────────────────────

// TestOrchestrator_RunResilient_StageReturnsNilAsset_GracefulDegradation
// pins the defensive-path branch `else if staged == nil` in
// orchestrator.go's Step 3. A well-behaved SourceStager SHOULD NOT
// return (nil, nil) — that violates the
// `assets.SourceStager.StageSource` contract — but the orchestrator
// has defensive code that treats the degenerate case as a soft failure
// (Warn + continue, no defer, no NPE) so a misbehaving port cannot
// panic the run.
func TestOrchestrator_RunResilient_StageReturnsNilAsset_GracefulDegradation(t *testing.T) {
	t.Skip("DEPRECATED: pins the OLD graceful-degradation contract (nil err on nil-asset return). Superseded by stock_fake_availability_test.go::TestStockStageSourcesStep_AllSourcesNilAsset_ReturnsTypedError which pins the NEW fail-closed contract (ErrStockStageSourcesAllFailed). Unskip when deferred Commit 6 (PR-STOCK-SOURCESTAGER-WIRE) reconciles the contracts.")
	rec := &recordingStager{
		stagedAsset: nil,
		stageErr:    nil, // intentionally defensive-path input
	}
	o := newWiringTestOrchestrator(rec)

	summary, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		FolderID:      "wf-nilasset",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if err != nil {
		t.Fatalf("RunResilient err on nil-asset defensive path: %v (defensive branch must not propagate)", err)
	}
	if summary == nil || summary.Manifest == nil {
		t.Fatal("RunResilient returned nil RunSummary.Manifest on nil-asset defensive path")
	}
	if rec.stageCalls != 1 {
		t.Errorf("StageSource called %d times, want 1", rec.stageCalls)
	}
	if rec.cleanupCalls != 0 {
		t.Errorf("Cleanup called %d times on nil-asset stage return, want 0 (no asset to clean)", rec.cleanupCalls)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (5) — empty input → no stage invocation (plan errors first)
// ─────────────────────────────────────────────────────────────────────

// TestOrchestrator_RunResilient_NoSources_NoStageInvocation pins the
// orchestrator step ladder order:
//
//	plan_clips (Step 2) → stage_sources (Step 3) → build_manifest (Step 4)
//
// When firstSource(input) returns false (both DirectURLs and
// SearchQueries empty), plan_clips emits the typed "no sources to
// plan" sentinel and returns BEFORE stage_sources runs. A regression
// that reorders the ladder (stage before plan) would issue futile
// StageSource calls against an empty input — wasting a port
// invocation and possibly leaking pre-flight signals.
func TestOrchestrator_RunResilient_NoSources_NoStageInvocation(t *testing.T) {
	rec := &recordingStager{
		stagedAsset: &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", Bytes: 1024},
	}
	o := newWiringTestOrchestrator(rec)

	_, err := o.RunResilient(context.Background(), &RunInput{
		// DirectURLs + SearchQueries intentionally empty
		FolderID:      "wf-empty",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if err == nil {
		t.Fatal("RunResilient returned nil err with empty sources (must surface the plan_clips 'no sources to plan' sentinel)")
	}
	if rec.stageCalls != 0 {
		t.Errorf("StageSource called %d times with empty sources, want 0 (plan_clips errors first; stage_sources must not run)", rec.stageCalls)
	}
	if rec.cleanupCalls != 0 {
		t.Errorf("Cleanup called %d times with empty sources, want 0 (no stage → no cleanup)", rec.cleanupCalls)
	}
}

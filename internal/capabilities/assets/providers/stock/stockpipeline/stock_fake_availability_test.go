// Package stockpipeline — stock_fake_availability_test.go (PR-STOCK-FAKE-AVAILABILITY-REMOVAL, 2026-07-04).
//
// Forward-coverage marker for the canonical fail-closed contract
// pinned on StockStageSourcesStep + StockComposeChunksStep. Mirrors
// the godlike/07 no-fake-availability invariant: a job that
// advertises the pipeline as "wired" must NOT report SUCCEEDED
// with zero artifacts on Drive + zero Qdrant projections + zero
// manifest. The 5 test functions below pin the contract:
//
//	(1) stager wired + all sources fail → ErrStockStageSourcesAllFailed
//	(2) stager wired + all sources succeed → nil err (happy path)
//	(3) stager wired + all sources return (nil, nil) →
//	    ErrStockStageSourcesAllFailed (defensive nil-asset path)
//	(4) renderer wired + all renders fail → ErrStockComposeChunksAllFailed
//	(5) renderer nil (test-fixture compat) → nil err
//
// STATUS (July 2026): tests are ACTIVE. The fail-closed gates
// landed in orchestrator_steps.go at the same commit. Any future
// regression that reverts the gates to Log+return nil will fail
// these tests at unit-test time.
//
// The existing 5 t.Skip()-gated tests in stock_stager_wiring_test.go
// are audit-pins for the deferred Commit 6 (STOCK-CUT-6
// forward-pointer). They pin the OLD contract (graceful degradation
// returning nil on all-failure). The tests in THIS file pin the NEW
// fail-closed contract. Both contracts cannot coexist; future
// Commit 6 wire-up must reconcile them (the deferred PR is expected
// to remove the old t.Skip()-gated tests and replace with the
// contracts in this file — see the PR-STOCK-SOURCESTAGER-WIRE
// forward-pointer in architecture/current.yaml).
package assets

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// mapStager is a test stub that delegates StageSource decisions to a
// per-URL handler function. This lets tests express per-URL
// success / failure / nil-asset outcomes without modifying the
// production-side stager.
//
// Compile-time port-drift defense: if assets.SourceStager's signature
// changes, this stub fails to compile and the test binary fails to
// link — the test signals the breakage before runtime.
type mapStager struct {
	handler func(assets.SourceRef) (*assets.StagedAsset, error)
}

var _ acquisition.SourceStager = (*mapStager)(nil)

func (m *mapStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	ref := assets.SourceRef{URL: req.Source.URL, DownloadSection: req.Source.DownloadSection, ForceKeyframes: req.Source.ForceKeyframes, MergeFormat: req.Source.MergeFormat}
	sa, err := m.handler(ref)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, nil
	}
	return &acquisition.PrepareContext{
		ID:           sa.SourceID,
		LocalPath:    sa.LocalPath,
		SizeBytes:    sa.Bytes,
		CleanupToken: sa.LocalPath,
	}, nil
}

func (m *mapStager) Release(_ context.Context, _ string) error { return nil }

// mapCutter is a test stub that delegates Cut decisions to a
// per-request handler. Returns CutBatchResult with the
// SuccessfulItems populated as Succeeded (so downstream
// extract_clips picks them up as CutPaths).
type mapCutter struct {
	handler func(CutRequest) (CutBatchResult, error)
}

var _ VideoCutter = (*mapCutter)(nil)

func (m *mapCutter) Cut(ctx context.Context, req CutRequest) (CutBatchResult, error) {
	return m.handler(req)
}

// mapRenderer is a test stub that delegates Render decisions to a
// per-request handler. Returns RenderResult + error. The callCount
// field tracks the number of Render invocations so tests can assert
// on per-chunk graceful degradation (a chunk failing should not
// abort subsequent chunks).
type mapRenderer struct {
	handler   func(RenderRequest) (RenderResult, error)
	callCount int
}

var _ StockRenderer = (*mapRenderer)(nil)

func (m *mapRenderer) Render(ctx context.Context, req RenderRequest) (RenderResult, error) {
	m.callCount++
	return m.handler(req)
}

// newFakeAvailOrchestrator constructs an Orchestrator wired with
// the given stubs. Both ArtifactPreparation + JobFinalizer are nil
// (test-fixture mode) so the orchestrator's asymmetric wiring gate
// passes and the post-step AssertRunSummaryArtifactsRequired gate
// is skipped. The pipeline runs through stock.plan → stage_sources
// → extract_clips → compose_chunks → publish → finalize with
// the per-step test-fixture-compat paths engaged where stubs are
// nil.
//
// IMPORTANT: per RunResilient's ErrOrchestratorNilDeps gate
// (orchestrator.go), the stager MUST be non-nil. The
// StockStageSourcesStep's nil-stager defensive path is therefore
// unreachable through RunResilient; it's a guard for direct
// step.Run callers only. This is why the tests below always pass
// a non-nil mapStager (even if its handler returns errors).
func newFakeAvailOrchestrator(stager acquisition.SourceStager, cutter VideoCutter, renderer StockRenderer) *Orchestrator {
	return NewTestStockOrchestrator(
		OrchestratorConfig{
			JobId:            "fake-avail-test",
			Lease:            testLease("fake-avail-test"),
			PolicyVersion:    "v1",
			ChunkDurationSec: 5,
			ClipDurationSec:  5,
		},
		NewDeterministicPlanner(),
		stager,
		func() VideoCutter {
			if cutter != nil {
				return cutter
			}
			return fakeSucceedingCutter{}
		}(),
		renderer,
	).
		WithAssetPreparation(&recordingArtifactPreparation{}).
		WithJobFinalizer(stubJobFinalizer{}).
		WithLocalFS(newRealishFakeLocalFS())
}

// successNoopRenderer is a mapRenderer that returns success and
// writes a small output file at req.OutputPath so the downstream
// publish step can hash it. Used by tests that exercise the pipeline
// without asserting on render behavior.
func successNoopRenderer() *mapRenderer {
	return &mapRenderer{handler: func(req RenderRequest) (RenderResult, error) {
		if req.OutputPath != "" {
			_ = os.WriteFile(req.OutputPath, []byte("rendered"), 0o644)
		}
		return RenderResult{}, nil
	}}
}

// ─────────────────────────────────────────────────────────────────────
// Test (1) — stager wired + all sources fail → ErrStockStageSourcesAllFailed
// ─────────────────────────────────────────────────────────────────────

// TestStockStageSourcesStep_AllSourcesFailed_ReturnsTypedError pins the
// canonical fail-closed contract for StockStageSourcesStep when
// every source in the plan fails to stage. The orchestrator MUST
// surface ErrStockStageSourcesAllFailed as a job failure (NOT nil
// error with zero staged assets).
//
// Without this test, a regression that reverts the fail-closed gate
// to Log+return nil would silently pass — the job would report
// SUCCEEDED with zero staged assets on Drive, zero Qdrant
// projections, and zero manifest chunks. godlike/07
// no-fake-availability regression.
func TestStockStageSourcesStep_AllSourcesFailed_ReturnsTypedError(t *testing.T) {
	stager := &mapStager{handler: func(ref assets.SourceRef) (*assets.StagedAsset, error) {
		return nil, errors.New("simulated stage failure (yt-dlp offline)")
	}}
	o := newFakeAvailOrchestrator(stager, nil, successNoopRenderer())

	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4"},
		FolderID:      "wf-all-fail",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if !errors.Is(err, ErrStockStageSourcesAllFailed) {
		t.Fatalf("expected ErrStockStageSourcesAllFailed, got %v (fail-closed contract violated — job would silently succeed with zero staged assets)", err)
	}
	if !strings.Contains(err.Error(), "https://example.com/a.mp4:") {
		t.Fatalf("all-failed error must expose the failed source key, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (2) — stager wired + all sources succeed → nil err (happy path)
// ─────────────────────────────────────────────────────────────────────

// TestStockStageSourcesStep_AllSourcesSucceed_NoError pins the
// canonical happy-path contract: when the stager succeeds, the
// fail-closed gate does NOT fire (len(staged) > 0) and the
// pipeline proceeds normally. Without this test, a regression
// that tightened the fail-closed gate to fire unconditionally
// would silently break every happy-path test in the package.
func TestStockStageSourcesStep_AllSourcesSucceed_NoError(t *testing.T) {
	stager := &mapStager{handler: func(ref assets.SourceRef) (*assets.StagedAsset, error) {
		return &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", SourceID: ref.URL, Bytes: 1024}, nil
	}}
	o := newFakeAvailOrchestrator(stager, nil, successNoopRenderer())

	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4"},
		FolderID:      "wf-all-succeed",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if err != nil {
		t.Fatalf("all-sources success should not fail, got %v (fail-closed gate must NOT fire when len(staged) > 0)", err)
	}
}

// TestStockStageSourcesStep_PartialFailure_ReturnsTypedError prevents a
// multi-video request from being reported as successful when only a subset
// of its sources could be downloaded.
func TestStockStageSourcesStep_PartialFailure_ReturnsTypedError(t *testing.T) {
	stager := &mapStager{handler: func(ref assets.SourceRef) (*assets.StagedAsset, error) {
		if strings.Contains(ref.URL, "/b.mp4") {
			return nil, errors.New("simulated yt-dlp bot challenge")
		}
		return &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", SourceID: ref.URL, Bytes: 1024}, nil
	}}
	o := newFakeAvailOrchestrator(stager, nil, successNoopRenderer())

	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4", "https://example.com/b.mp4"},
		FolderID:      "wf-partial-fail",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if !errors.Is(err, ErrStockStageSourcesIncomplete) {
		t.Fatalf("expected ErrStockStageSourcesIncomplete, got %v", err)
	}
	if !strings.Contains(err.Error(), "https://example.com/b.mp4: simulated yt-dlp bot challenge") {
		t.Fatalf("partial error must expose the failed source and reason, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (3) — stager wired + all sources return (nil, nil) →
//            ErrStockStageSourcesAllFailed (defensive nil-asset path)
// ─────────────────────────────────────────────────────────────────────

// TestStockStageSourcesStep_AllSourcesNilAsset_ReturnsTypedError pins
// the fail-closed contract for the defensive nil-asset path:
// when the stager returns (nil, nil) for every source (a degenerate
// but possible port-misbehavior), the step takes the
// `else if sa == nil` branch in the per-source loop. With zero
// staged assets accumulated, the fail-closed gate fires.
//
// Without this test, a regression that reverted ONLY the
// `stageErr != nil` branch's fail-closed behavior (and forgot
// the `sa == nil` branch) would silently pass the
// TestStockStageSourcesStep_AllSourcesFailed test (which uses
// `stageErr`) but break this test (which uses the nil-asset
// path).
func TestStockStageSourcesStep_AllSourcesNilAsset_ReturnsTypedError(t *testing.T) {
	stager := &mapStager{handler: func(ref assets.SourceRef) (*assets.StagedAsset, error) {
		// Intentionally degenerate: non-nil err=nil + non-nil asset=nil.
		// Violates the SourceStager contract (StageSource should
		// return non-nil asset on nil err) but the orchestrator
		// has defensive code for this case.
		return nil, nil
	}}
	o := newFakeAvailOrchestrator(stager, nil, successNoopRenderer())

	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4"},
		FolderID:      "wf-nil-asset",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if !errors.Is(err, ErrStockStageSourcesAllFailed) {
		t.Fatalf("expected ErrStockStageSourcesAllFailed, got %v (defensive nil-asset path: fail-closed gate must fire when len(staged) == 0 after the nil-asset branch)", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (4) — renderer wired + all renders fail → ErrStockComposeChunksAllFailed
// ─────────────────────────────────────────────────────────────────────

// TestStockComposeChunksStep_AllRendersFailed_ReturnsTypedError pins
// the canonical fail-closed contract for StockComposeChunksStep
// when every chunk fails to render. The orchestrator MUST surface
// ErrStockComposeChunksAllFailed as a job failure (NOT nil error
// with zero composed chunks).
//
// Mirrors Test (1) for the compose step. The pipeline wiring for
// this test is: stager succeeds (produces 1 StagedAsset) → cutter
// succeeds (produces 1 CutPath) → renderer fails (all renders
// return error) → StockComposeChunksStep returns
// ErrStockComposeChunksAllFailed.
func TestStockComposeChunksStep_AllRendersFailed_ReturnsTypedError(t *testing.T) {
	stager := &mapStager{handler: func(ref assets.SourceRef) (*assets.StagedAsset, error) {
		return &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", SourceID: ref.URL, Bytes: 1024}, nil
	}}
	cutter := &mapCutter{handler: func(req CutRequest) (CutBatchResult, error) {
		items := make([]CutItemResult, len(req.Jobs))
		for i, j := range req.Jobs {
			items[i] = CutItemResult{
				JobID:      j.OutputPath,
				OutputPath: j.OutputPath,
				Status:     CutItemStatusSucceeded,
				SizeBytes:  1024,
			}
		}
		return CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
	}}
	renderer := &mapRenderer{handler: func(req RenderRequest) (RenderResult, error) {
		return RenderResult{}, errors.New("simulated render failure (ffmpeg error)")
	}}
	o := newFakeAvailOrchestrator(stager, cutter, renderer)

	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4"},
		FolderID:      "wf-render-fail",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if !errors.Is(err, ErrStockComposeChunksAllFailed) {
		t.Fatalf("expected ErrStockComposeChunksAllFailed, got %v (fail-closed contract violated — job would silently succeed with zero composed chunks)", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Test (5) — renderer nil (test-fixture compat) → nil err
// ─────────────────────────────────────────────────────────────────────

// TestStockComposeChunksStep_NilRenderer_ReturnsErrOrchestratorNilDeps pins
// the composition-time fail-closed contract (PR-STOCK-PRODUCTION-DEPS,
// July 2026): when the renderer is nil, the orchestrator's
// RunResilient gate (orchestrator.go::RunResilient) MUST surface
// ErrOrchestratorNilDeps BEFORE any step body runs. The previous
// runtime nil-check (StockComposeChunksStep's test-fixture compat
// path) is RETIRED per godlike/07 no-fake-availability: a
// production run must NOT reach the step body with a nil renderer,
// and a test fixture that passes nil must update to wire a non-nil
// stub (mapRenderer) per the production contract.
//
// A regression that re-introduces the runtime nil-check (silently
// succeeding with nil renderer) would violate the fail-closed
// composition-time contract and would be caught by this test.
func TestStockComposeChunksStep_NilRenderer_ReturnsErrOrchestratorNilDeps(t *testing.T) {
	stager := &mapStager{handler: func(ref assets.SourceRef) (*assets.StagedAsset, error) {
		return &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", SourceID: ref.URL, Bytes: 1024}, nil
	}}
	cutter := &mapCutter{handler: func(req CutRequest) (CutBatchResult, error) {
		items := make([]CutItemResult, len(req.Jobs))
		for i, j := range req.Jobs {
			items[i] = CutItemResult{
				JobID:      j.OutputPath,
				OutputPath: j.OutputPath,
				Status:     CutItemStatusSucceeded,
				SizeBytes:  1024,
			}
		}
		return CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
	}}
	// Renderer intentionally nil — the orchestrator's composition-time
	// gate MUST reject with ErrOrchestratorNilDeps before the step body
	// runs (godlike/07 no-fake-availability: production must NOT reach
	// StockComposeChunksStep.Run with a nil renderer).
	o := newFakeAvailOrchestrator(stager, cutter, nil)

	_, err := o.RunResilient(context.Background(), &RunInput{
		DirectURLs:    []string{"https://example.com/a.mp4"},
		FolderID:      "wf-nil-renderer",
		ClipDuration:  5,
		ChunkDuration: 5,
	})

	if !errors.Is(err, ErrOrchestratorNilDeps) {
		t.Fatalf("nil renderer must surface ErrOrchestratorNilDeps (composition-time fail-closed gate), got %v (fail-closed contract violated — a regression that re-introduces the runtime nil-check would silently succeed with nil renderer)", err)
	}
}

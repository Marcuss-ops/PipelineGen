// Package stockpipeline — stock_stager_wiring_test.go.
//
// Active wiring coverage for the canonical acquisition.SourceStager
// integration in Orchestrator.RunResilient. Fail-closed staging failures and
// nil-asset outcomes are covered by stock_fake_availability_test.go; this file
// keeps only the live orchestration invariants: invocation, cleanup, and step
// ordering.
package stockpipeline

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
)

// recordingStager captures Prepare + Release calls through the canonical
// acquisition.SourceStager port without dragging the real yt-dlp path into
// unit tests.
type recordingStager struct {
	stageCalls    int
	lastRef       assets.SourceRef
	stagedAsset   *assets.StagedAsset
	cleanupCalls  int
	cleanedStaged *assets.StagedAsset
}

var _ acquisition.SourceStager = (*recordingStager)(nil)

func (r *recordingStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	r.stageCalls++
	r.lastRef = assets.SourceRef{URL: req.Source.URL, DownloadSection: req.Source.DownloadSection, ForceKeyframes: req.Source.ForceKeyframes, MergeFormat: req.Source.MergeFormat}
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

func (r *recordingStager) Release(_ context.Context, cleanupToken string) error {
	r.cleanupCalls++
	r.cleanedStaged = &assets.StagedAsset{LocalPath: cleanupToken}
	return nil
}

func newWiringTestOrchestrator(rec *recordingStager) *Orchestrator {
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

func TestOrchestrator_RunResilient_NoSources_NoStageInvocation(t *testing.T) {
	rec := &recordingStager{
		stagedAsset: &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", Bytes: 1024},
	}
	o := newWiringTestOrchestrator(rec)

	_, err := o.RunResilient(context.Background(), &RunInput{
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

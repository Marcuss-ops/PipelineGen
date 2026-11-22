package texttracks

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// TestRunClassifiesSourceMisses pins the report taxonomy: a clip whose source
// could not be produced is a FAILED candidate, counted once. The three labels
// used to be gated on a non-nil per-asset error, but ProcessAsset reports a
// missing source as data (nil error), so every case was dead and a subtitle
// miss was counted as neither failed nor acquired.
func TestRunClassifiesSourceMisses(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ob := &fakeOutbox{}

	m := newTestMaterializer(t, repo, &fakeTranslator{}, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	svc := newProcessAssetService(t, repo, m, &stubSubtitles{}, &stubWhisper{})
	svc.clips = &clipsListerStub{item: processAssetItem()}
	svc.log = zap.NewNop()

	report, err := svc.Run(ctx, nil, processAssetOptions())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Processed != 1 {
		t.Fatalf("Processed = %d, want 1", report.Processed)
	}
	if report.SourceMissingCount != 1 {
		t.Fatalf("SourceMissingCount = %d, want 1 (no source label must be classified)", report.SourceMissingCount)
	}
	if len(report.FailedAssetIDs) != 1 || report.FailedAssetIDs[0] != "asset-1" {
		t.Fatalf("FailedAssetIDs = %v, want [asset-1]", report.FailedAssetIDs)
	}
	if report.PerAsset[0].Err != "no_source_track" {
		t.Fatalf("per-asset label = %q, want no_source_track", report.PerAsset[0].Err)
	}
}

// TestRunCountsReadyAssetAsHealthy guards the other side of the switch: a
// classified label must not make every processed asset look failed.
func TestRunCountsReadyAssetAsHealthy(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ob := &fakeOutbox{}
	// The full repair-pass shape: source and every target already READY with
	// timed cues, so Priority 1 satisfies the clip without the network.
	seedFullyTranslatedAssetFor(repo, "asset-1", "il clip ha gia il transcript")
	repo.cues[key("asset-1", "en", detail.TextTrackTranscript)] = []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "the clip already has it"},
	}
	repo.cues[key("asset-1", "it", detail.TextTrackTranscript)] = []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "il clip ce l'ha gia"},
	}

	m := newTestMaterializer(t, repo, &fakeTranslator{}, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	svc := newProcessAssetService(t, repo, m, &stubSubtitles{}, &stubWhisper{})
	svc.clips = &clipsListerStub{item: processAssetItem()}
	svc.log = zap.NewNop()

	report, err := svc.Run(ctx, nil, processAssetOptions())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SourceMissingCount != 0 || len(report.FailedAssetIDs) != 0 {
		t.Fatalf("a READY transcript must not be classified as a failure: missing=%d failed=%v",
			report.SourceMissingCount, report.FailedAssetIDs)
	}
	if report.SourceReadyCount != 1 {
		t.Fatalf("SourceReadyCount = %d, want 1", report.SourceReadyCount)
	}
}

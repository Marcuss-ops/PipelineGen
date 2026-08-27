package stockplan

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPartialDownloadPlanUsesOnlySelectedSourceWindow(t *testing.T) {
	plan := PartialDownloadPlan{
		VideoID:        "poland-1939",
		StartMs:        151000,
		EndMs:          161000,
		DurationMs:     10000,
		ProfileVersion: "youtube-stock-v1",
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if got, want := plan.YTDLPSection(), "*151.000-161.000"; got != want {
		t.Fatalf("yt-dlp section = %q, want %q", got, want)
	}
	if plan.EndMs-plan.StartMs != plan.DurationMs {
		t.Fatalf("window duration = %d, want %d", plan.EndMs-plan.StartMs, plan.DurationMs)
	}

	fullVideo := PartialDownloadPlan{VideoID: plan.VideoID, StartMs: 0, EndMs: 2700000, DurationMs: 2700000, ProfileVersion: plan.ProfileVersion}
	if plan.CacheKey() == fullVideo.CacheKey() {
		t.Fatal("partial window must not share cache identity with the full video")
	}
}

func TestPartialDownloadPlanRejectsMismatchedDuration(t *testing.T) {
	plan := PartialDownloadPlan{VideoID: "video-1", StartMs: 151000, EndMs: 161000, DurationMs: 9000}
	if err := plan.Validate(); err == nil {
		t.Fatal("expected mismatched duration to be rejected")
	}
}

func TestYouTubeSourceTimelineDoesNotBecomeRenderTimeline(t *testing.T) {
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID: "asset-poland-1939", Provider: scriptpkg.VidRushProviderYouTube,
		SourceURL:     "https://www.youtube.com/watch?v=poland-1939",
		SourceStartMs: 151000, SourceEndMs: 161000, DurationMs: 10000,
	}
	binding := scriptpkg.ClipBinding{
		ClipID: candidate.AssetID, DriveLink: "https://drive.google.com/file/d/asset-poland-1939",
		StartMs: 43000, EndMs: 53000, DurationMs: 10000,
	}

	if candidate.SourceStartMs != 151000 || candidate.SourceEndMs != 161000 {
		t.Fatalf("source provenance changed: %+v", candidate)
	}
	if binding.StartMs != 43000 || binding.EndMs != 53000 {
		t.Fatalf("render timeline changed: %+v", binding)
	}
	if candidate.SourceStartMs == binding.StartMs || candidate.SourceEndMs == binding.EndMs {
		t.Fatal("source timeline was conflated with render timeline")
	}
	if binding.DurationMs != candidate.DurationMs {
		t.Fatalf("clip duration = %d, want %d", binding.DurationMs, candidate.DurationMs)
	}
}

package adapters

import (
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"

	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestNormalizeClipDurationMsUsesTargetAndBounds(t *testing.T) {
	tests := []struct {
		name string
		req  scriptports.VidRushSearchRequest
		want int64
	}{
		{name: "target", req: scriptports.VidRushSearchRequest{TargetDurationMs: 8420}, want: 8420},
		{name: "scene fallback", req: scriptports.VidRushSearchRequest{SceneDurationMs: 6300}, want: 6300},
		{name: "estimated fallback", req: scriptports.VidRushSearchRequest{EstimatedDurationMs: 5100}, want: 5100},
		{name: "min bound", req: scriptports.VidRushSearchRequest{TargetDurationMs: 2000, MinDurationMs: 4000}, want: 4000},
		{name: "max bound", req: scriptports.VidRushSearchRequest{TargetDurationMs: 15000, MaxDurationMs: 12000}, want: 12000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeClipDurationMs(tc.req); got != tc.want {
				t.Fatalf("normalizeClipDurationMs() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelectedSegmentsToCandidatesPreservesProvenanceAndStatus(t *testing.T) {
	got := selectedSegmentsToCandidates([]stockplan.SelectedSegment{{
		YouTubeVideoID: "video-1", SourceURL: "https://www.youtube.com/watch?v=video-1",
		StartMs: 151000, EndMs: 161000, DurationMs: 10000,
		RelevanceScore: .92, SelectionReason: "transcript match",
		Status: "processed", AssetID: "asset_xyz", DriveLink: "https://drive.google/clip",
		LocalPath: "/tmp/clip.mp4",
	}}, "German invasion Poland")
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1", len(got))
	}
	candidate := got[0]
	if candidate.Provider != scriptpkg.VidRushProviderYouTube || candidate.SourceStartMs != 151000 || candidate.SourceEndMs != 161000 {
		t.Fatalf("provenance not preserved: %+v", candidate)
	}
	if candidate.AcquisitionStatus != "acquired" || candidate.VerificationStatus != "verified" || candidate.PersistenceStatus != "persisted" {
		t.Fatalf("status not mapped: %+v", candidate)
	}
	if candidate.AssetID != "asset_xyz" || candidate.DriveLink == "" || candidate.LocalPath == "" {
		t.Fatalf("asset fields not preserved: %+v", candidate)
	}
}

func TestSelectedSegmentsToCandidatesKeepsPlannedStatus(t *testing.T) {
	got := selectedSegmentsToCandidates([]stockplan.SelectedSegment{{
		YouTubeVideoID: "video-1", SourceURL: "https://www.youtube.com/watch?v=video-1",
		StartMs: 1000, EndMs: 11000, DurationMs: 10000,
		RelevanceScore: .5, SelectionReason: "planned", Status: "SEGMENTS_PLANNED",
	}}, "query")
	if len(got) != 1 || got[0].AcquisitionStatus != "planned" || got[0].PersistenceStatus != "pending" {
		t.Fatalf("planned status not preserved: %+v", got)
	}
}

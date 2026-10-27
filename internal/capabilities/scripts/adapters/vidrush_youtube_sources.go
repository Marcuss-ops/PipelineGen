package adapters

import (
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The YouTube source-hint helpers of the VidRush provider fan-out live here,
// beside the fan-out that consumes them. They answer one question — "which
// YouTube source does this segment carry" — for vidrush_registry_searchers.go.
// The unused VidRushYouTubeProvider adapter and its tests were removed with
// the dead-code wave.

func youtubeSourcesForSegment(plan *scriptpkg.ResolvedGenerationPlan, segmentID string) []scriptports.VidRushSourceHint {
	if plan == nil {
		return nil
	}
	out := make([]scriptports.VidRushSourceHint, 0)
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID != segmentID || !strings.EqualFold(source.Provider, scriptpkg.VidRushProviderYouTube) {
			continue
		}
		out = append(out, scriptports.VidRushSourceHint{URL: source.SourceURL, Priority: source.Priority, Required: string(source.Mode) == "required"})
	}
	return out
}

func youtubeSourceRequired(plan *scriptpkg.ResolvedGenerationPlan, segmentID string) bool {
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID == segmentID && strings.EqualFold(source.Provider, scriptpkg.VidRushProviderYouTube) && source.Mode == "required" {
			return true
		}
	}
	return false
}

func youtubeQuery(segment scriptpkg.VidRushSegmentResult) string {
	if len(segment.Insights.YouTubeQueries) > 0 {
		return strings.Join(segment.Insights.YouTubeQueries, " ")
	}
	return segment.Text
}

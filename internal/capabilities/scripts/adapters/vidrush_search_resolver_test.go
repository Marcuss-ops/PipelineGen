package adapters

import (
	"testing"

	media "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestResolveManualSegmentQueries_UsesCallerSegmentQuery(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{Searches: []media.SegmentMediaSearch{{
		SegmentID: "main", Slot: media.SlotPrimaryVideo,
		Query: "ancient Maya temples jungle aerial cinematic", Providers: []string{"youtube"}, MediaTypes: []string{"video"}, Limit: 8,
	}}}}

	queries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: "main", Text: "Testo sulla civiltà Maya"}, scriptpkg.VidRushProviderYouTube, media.SlotPrimaryVideo)
	require.Equal(t, []string{"ancient Maya temples jungle aerial cinematic"}, queries)
}

func TestResolveManualSegmentQueries_DoesNotLeakOtherSegments(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{Searches: []media.SegmentMediaSearch{{
		SegmentID: "other", Slot: media.SlotPrimaryVideo, Query: "Roman empire battle", Providers: []string{"youtube"},
	}}}}

	queries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: "main"}, scriptpkg.VidRushProviderYouTube, media.SlotPrimaryVideo)
	require.Empty(t, queries)
}

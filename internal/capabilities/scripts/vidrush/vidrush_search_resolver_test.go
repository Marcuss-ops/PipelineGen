package vidrush

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

func TestManualSearchSlotsCoversEveryExplicitSlot(t *testing.T) {
	slots := []media.SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage, media.SlotEvidenceOverlay, media.SlotPortrait, media.SlotDocument, media.SlotBackground, media.SlotMap}
	plan := media.MediaPlanSpec{}
	for _, slot := range slots {
		plan.Searches = append(plan.Searches, media.SegmentMediaSearch{SegmentID: "main", Slot: slot, Query: string(slot)})
	}
	got := ManualSearchSlots(plan, "main")
	require.Equal(t, slots, got)
	require.Empty(t, ManualSearchSlots(plan, "other"))
}

func TestResolveManualSegmentQueries_DoesNotLeakOtherSegments(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: media.MediaPlanSpec{Searches: []media.SegmentMediaSearch{{
		SegmentID: "other", Slot: media.SlotPrimaryVideo, Query: "Roman empire battle", Providers: []string{"youtube"},
	}}}}

	queries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: "main"}, scriptpkg.VidRushProviderYouTube, media.SlotPrimaryVideo)
	require.Empty(t, queries)
}

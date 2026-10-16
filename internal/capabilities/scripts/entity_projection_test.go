package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestAggregateEntityResultClassifiesExactlyOnce certifies the durable entity
// projection: PERSON → persons, LOCATION/PLACE/COUNTRY/CITY → places, every
// other type → concepts. This is the durable-surface mirror of the legacy
// Artifacts.Entities classification.
func TestAggregateEntityResultClassifiesExactlyOnce(t *testing.T) {
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-0", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Jackie Chan", Type: "PERSON", Confidence: 0.95},
			{Value: "Hong Kong", Type: "LOCATION", Confidence: 0.9},
			{Value: "martial arts", Type: "CONCEPT", Confidence: 0.85},
		}}},
		{SegmentID: "seg-1", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Jackie Chan", Type: "PERSON", Confidence: 0.9},
			{Value: "London", Type: "CITY", Confidence: 0.8},
			{Value: "filmmaking", Type: "CONCEPT", Confidence: 0.7},
		}}},
	}

	agg := aggregateEntityResult(segments)
	require.NotNil(t, agg)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Jackie Chan", Type: "PERSON", Score: 0.95}, {Value: "Jackie Chan", Type: "PERSON", Score: 0.9}}, agg.Persons)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Hong Kong", Type: "LOCATION", Score: 0.9}, {Value: "London", Type: "CITY", Score: 0.8}}, agg.Places)
	assert.Equal(t, []scriptpkg.Entity{{Value: "martial arts", Type: "CONCEPT", Score: 0.85}, {Value: "filmmaking", Type: "CONCEPT", Score: 0.7}}, agg.Concepts)
}

// TestAggregateEntityResultNilWhenEmpty certifies the aggregate is nil (not an
// empty block) when no segment produced any entity — the durable surface then
// omits the block entirely.
func TestAggregateEntityResultNilWhenEmpty(t *testing.T) {
	assert.Nil(t, aggregateEntityResult(nil))
	assert.Nil(t, aggregateEntityResult([]scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-0", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "  ", Type: "PERSON"}}}},
	}))
}

// TestAggregateEntityResultMatchesLegacyBuckets certifies that the PLACE and
// COUNTRY synonyms fall into the places bucket exactly like the legacy
// mergeVidRushAggregate switch.
func TestAggregateEntityResultMatchesLegacyBuckets(t *testing.T) {
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-0", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Paris", Type: "PLACE"},
			{Value: "France", Type: "COUNTRY"},
			{Value: "Serena Williams", Type: "PERSON"},
		}}},
	}
	agg := aggregateEntityResult(segments)
	require.NotNil(t, agg)
	assert.Len(t, agg.Persons, 1)
	assert.Len(t, agg.Places, 2)
	assert.Len(t, agg.Concepts, 0)
}

// TestAssignEntityImageMotionsRequestedPoolHonorsOffset certifies the
// run-level entity_image_motions payload rotates offset-aware like the
// automatic pool and the caption pool. Regression: the requested pool was
// walked from index 0 on every run, so the same payload entrances recurred
// in the same order no matter the per-run motion offset.
func TestAssignEntityImageMotionsRequestedPoolHonorsOffset(t *testing.T) {
	pool := []string{
		"image_fade_reveal", "image_focus_reveal", "image_scale_reveal",
		"image_slide_left_reveal", "image_slide_right_reveal",
	}
	items := func() []capabilityoverlay.OverlayItem {
		return []capabilityoverlay.OverlayItem{
			{ID: "scene-1-image-a", SceneID: "scene-1", Kind: string(capabilityoverlay.KindEntityImage), EntityCaption: "Chiara Poggi"},
			{ID: "scene-1-image-b", SceneID: "scene-1", Kind: string(capabilityoverlay.KindEntityImage), EntityCaption: "Alberto Stasi"},
		}
	}
	run := func(offset int) []string {
		got := items()
		require.NoError(t, assignEntityImageMotions(got, offset, 1920, 1080, "", pool))
		return []string{got[0].MotionID, got[1].MotionID}
	}
	assert.Equal(t, []string{pool[0], pool[1]}, run(0))
	assert.Equal(t, []string{pool[2], pool[3]}, run(2))
	assert.Equal(t, run(0), run(0))
}

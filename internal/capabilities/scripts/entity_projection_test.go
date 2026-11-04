package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// The same person mentioned in two segments is ONE row: the aggregate
	// keeps the strongest score, never a duplicate mention.
	assert.Equal(t, []scriptpkg.Entity{{Value: "Jackie Chan", Type: "PERSON", Score: 0.95}}, agg.Persons)
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

// TestAggregateEntityResultUnifiesSurnameMentions certifies the entity
// selection is strict: a surname-only mention resolves onto the grounded full
// name of its segment, and an exact repeat keeps one row with the strongest
// score. "Stasi" must not become a second identity next to "Alberto Stasi".
func TestAggregateEntityResultUnifiesSurnameMentions(t *testing.T) {
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-0", Text: "Alberto Stasi fu assolto. Stasi era presente. Chiara Poggi parlò.",
			Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Alberto Stasi", Type: "PERSON", Confidence: 0.95},
				{Value: "Stasi", Type: "PERSON", Confidence: 0.9},
				{Value: "Chiara Poggi", Type: "PERSON", Confidence: 0.95},
				{Value: "Chiara Poggi", Type: "PERSON", Confidence: 0.8},
			}},
		},
	}
	agg := aggregateEntityResult(segments)
	require.NotNil(t, agg)
	assert.Equal(t, []scriptpkg.Entity{
		{Value: "Alberto Stasi", Type: "PERSON", Score: 0.95},
		{Value: "Chiara Poggi", Type: "PERSON", Score: 0.95},
	}, agg.Persons)
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

// TestAggregateEntityResultUnifiesSurnameAcrossSegments certifies the
// cross-segment case the same-segment expansion cannot see: scene 03 and 10
// say only "Stasi", while the full name is grounded in another scene.
func TestAggregateEntityResultUnifiesSurnameAcrossSegments(t *testing.T) {
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-0", Text: "Alberto Stasi fu assolto.",
			Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Alberto Stasi", Type: "PERSON", Confidence: 0.95},
			}}},
		{SegmentID: "seg-1", Text: "Stasi era presente.",
			Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Stasi", Type: "PERSON", Confidence: 0.9},
			}}},
	}
	agg := aggregateEntityResult(segments)
	require.NotNil(t, agg)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Alberto Stasi", Type: "PERSON", Score: 0.95}}, agg.Persons)
}

// TestAggregateEntityResultKeepsAmbiguousSurname certifies the fail-safe: two
// distinct full names share a surname, so the surname-only mention is not
// silently assigned to one of them.
func TestAggregateEntityResultKeepsAmbiguousSurname(t *testing.T) {
	segments := []scriptpkg.VidRushSegmentResult{
		{SegmentID: "seg-0", Text: "Mario Rossi parlò.", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "Mario Rossi", Type: "PERSON", Confidence: 0.9}}}},
		{SegmentID: "seg-1", Text: "Luca Rossi parlò.", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "Luca Rossi", Type: "PERSON", Confidence: 0.9}}}},
		{SegmentID: "seg-2", Text: "Rossi era presente.", Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{{Value: "Rossi", Type: "PERSON", Confidence: 0.8}}}},
	}
	agg := aggregateEntityResult(segments)
	require.NotNil(t, agg)
	assert.Len(t, agg.Persons, 3)
}

// TestAggregateEntityResultCanonicalizesPartialDates pins the date cleanup:
// partial surfaces collapse onto the unique full date, ambiguous "17 dicembre"
// is dropped, and bare years remain distinct display units.
func TestAggregateEntityResultCanonicalizesPartialDates(t *testing.T) {
	surfaces := []string{
		"13 agosto 2007", "13 agosto", "agosto 2007",
		"17 dicembre 2009", "17 dicembre", "dicembre 2009",
		"6 dicembre 2011", "6 dicembre", "dicembre 2011",
		"18 aprile 2013", "18 aprile", "aprile 2013",
		"17 dicembre 2014", "dicembre 2014",
		"12 dicembre 2015", "12 dicembre", "dicembre 2015",
		"2009", "2011", "2013", "2014", "2015",
	}
	entities := make([]scriptpkg.ExtractedEntity, 0, len(surfaces))
	for _, surface := range surfaces {
		entities = append(entities, scriptpkg.ExtractedEntity{Value: surface, Type: "DATE", Confidence: 0.7})
	}
	agg := aggregateEntityResult([]scriptpkg.VidRushSegmentResult{{SegmentID: "seg-0", Insights: scriptpkg.SegmentInsights{Entities: entities}}})
	require.NotNil(t, agg)
	want := map[string]bool{
		"13 agosto 2007": true, "17 dicembre 2009": true, "6 dicembre 2011": true,
		"18 aprile 2013": true, "17 dicembre 2014": true, "12 dicembre 2015": true,
		"2009": true, "2011": true, "2013": true, "2014": true, "2015": true,
	}
	assert.Len(t, agg.Concepts, len(want))
	for _, concept := range agg.Concepts {
		assert.True(t, want[concept.Value], "unexpected canonical date %q", concept.Value)
	}
}

// TestAggregateEntityResultNormalizesImportantPhrases pins the casing and
// sentence-boundary repair for the two selector artefacts seen in Garlasco.
func TestAggregateEntityResultNormalizesImportantPhrases(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-0",
		Text:      "Annullare con rinvio significa chiedere a un’altra sezione di rivalutare la causa. La pena divenne definitiva.",
		Insights: scriptpkg.SegmentInsights{ImportantPhrases: []string{
			"rinvio significa chiedere a un’altra sezione",
			"la pena divenne definitiva.",
		}},
	}
	agg := aggregateEntityResult([]scriptpkg.VidRushSegmentResult{seg})
	require.NotNil(t, agg)
	assert.Equal(t, []string{
		"Annullare con rinvio significa chiedere a un’altra sezione",
		"La pena divenne definitiva.",
	}, agg.ImportantPhrases)
}

// TestDropSurnameOnlyAnnotationDuplicates certifies the planner-side cleanup:
// a surname-only annotation disappears when exactly one full name exists in
// the run, while an ambiguous surname remains untouched.
func TestDropSurnameOnlyAnnotationDuplicates(t *testing.T) {
	annotations := map[int]*scriptpkg.SceneAnnotations{
		0: {PrimaryEntities: []scriptpkg.AnnotatedEntity{{Type: "PERSON", CanonicalName: "Alberto Stasi"}}},
		1: {PrimaryEntities: []scriptpkg.AnnotatedEntity{{Type: "PERSON", CanonicalName: "Stasi"}}},
		2: {PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{Type: "PERSON", CanonicalName: "Mario Rossi"},
			{Type: "PERSON", CanonicalName: "Luca Rossi"},
		}},
		3: {PrimaryEntities: []scriptpkg.AnnotatedEntity{{Type: "PERSON", CanonicalName: "Rossi"}}},
	}
	dropSurnameOnlyAnnotationDuplicates(annotations)
	require.NotNil(t, annotations[1])
	assert.Empty(t, annotations[1].PrimaryEntities)
	require.Len(t, annotations[2].PrimaryEntities, 2)
	require.Len(t, annotations[3].PrimaryEntities, 1)
	assert.Equal(t, "Rossi", annotations[3].PrimaryEntities[0].CanonicalName)
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
	items := func() []capmodel.OverlayItem {
		return []capmodel.OverlayItem{
			{ID: "scene-1-image-a", SceneID: "scene-1", Kind: string(capmodel.KindEntityImage), EntityCaption: "Chiara Poggi"},
			{ID: "scene-1-image-b", SceneID: "scene-1", Kind: string(capmodel.KindEntityImage), EntityCaption: "Alberto Stasi"},
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

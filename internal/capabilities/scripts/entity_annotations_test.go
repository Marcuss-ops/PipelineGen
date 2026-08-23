// Package scriptgeneration — entity_annotations_test.go certifies the durable
// per-scene entity→annotation projection: entities grounded in the scene text
// become primary (PERSON/ORG/GPE) or secondary (everything else) annotations,
// and the runner applies them to the matching scene after the VidRush barrier.
package scriptgeneration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProjectEntityAnnotations_GroundsAndClassifies(t *testing.T) {
	text := "Dwayne Johnson walked into Miami. Wrestling is his craft."
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-0",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Dwayne Johnson", Type: "PERSON", Confidence: 0.95},
				{Value: "Miami", Type: "LOCATION", Confidence: 0.9},
				{Value: "wrestling", Type: "CONCEPT", Confidence: 0.8},
				{Value: "absent entity", Type: "PERSON", Confidence: 0.5},
			},
		},
	}

	ann := projectEntityAnnotations(text, "en", seg)
	require.NotNil(t, ann)
	require.Equal(t, "completed", ann.Status)

	require.Len(t, ann.PrimaryEntities, 2)
	assert.Equal(t, "Dwayne Johnson", ann.PrimaryEntities[0].CanonicalName)
	assert.Equal(t, "PERSON", ann.PrimaryEntities[0].Type)
	assert.Equal(t, "Miami", ann.PrimaryEntities[1].CanonicalName)
	assert.Equal(t, "GPE", ann.PrimaryEntities[1].Type)

	require.Len(t, ann.SecondaryEntities, 1)
	assert.Equal(t, "wrestling", ann.SecondaryEntities[0].CanonicalName)
	assert.Equal(t, "CONCEPT", ann.SecondaryEntities[0].Type)

	// The absent entity never occurs in the text and must be skipped.
	for _, e := range append(ann.PrimaryEntities, ann.SecondaryEntities...) {
		assert.NotEqual(t, "absent entity", e.CanonicalName)
	}
}

func TestProjectEntityAnnotations_NilWhenNoGroundedEntity(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "ghost person", Type: "PERSON", Confidence: 0.5},
			},
		},
	}
	assert.Nil(t, projectEntityAnnotations("completely different text", "en", seg))
	assert.Nil(t, projectEntityAnnotations("", "en", seg))
}

func TestApplySegmentEntityAnnotations_MatchesScenes(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Dwayne Johnson in Miami."}},
			{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "A place without named people."}},
		},
	}
	segments := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-0", Position: 0, Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Dwayne Johnson", Type: "PERSON", Confidence: 0.95},
				{Value: "Miami", Type: "LOCATION", Confidence: 0.9},
			},
		}},
		{SceneID: "scene-1", Position: 1, Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "ghost", Type: "PERSON", Confidence: 0.5},
			},
		}},
	}

	applySegmentEntityAnnotations(result, "en", segments)

	require.NotNil(t, result.Scenes[0].Annotations)
	assert.Len(t, result.Scenes[0].Annotations.PrimaryEntities, 2)
	// scene-1 has no grounded entity → annotations stay nil (never faked).
	assert.Nil(t, result.Scenes[1].Annotations)
}

// TestProjectEntityAnnotations_ProjectsImportantPhrasesAndWords certifies
// the phrase/word projection: grounded important phrases/words become
// annotation spans (one phrase per scene, legacy descending word scores),
// ungrounded ones are skipped, and a segment whose only content is an
// ungrounded phrase still yields nil (never faked annotations).
func TestProjectEntityAnnotations_ProjectsImportantPhrasesAndWords(t *testing.T) {
	text := "Apple changed the market in Cupertino."
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-0",
		Insights: scriptpkg.SegmentInsights{
			ImportantPhrases: []string{"changed the market", "never grounded phrase"},
			ImportantWords:   []string{"market", "Apple"},
		},
	}

	ann := projectEntityAnnotations(text, "en", seg)
	require.NotNil(t, ann)
	require.Len(t, ann.ImportantPhrases, 1, "at most one important phrase per scene")
	assert.Equal(t, "changed the market", ann.ImportantPhrases[0].Text)
	assert.Equal(t, 0.80, ann.ImportantPhrases[0].Score)

	require.Len(t, ann.ImportantWords, 2)
	assert.Equal(t, "market", ann.ImportantWords[0].Text)
	assert.Equal(t, "Apple", ann.ImportantWords[1].Text)
	assert.Greater(t, ann.ImportantWords[0].Score, ann.ImportantWords[1].Score, "first word keeps the higher score")

	// A segment whose only content is an ungrounded phrase/word is a no-op.
	nilSeg := scriptpkg.VidRushSegmentResult{
		Insights: scriptpkg.SegmentInsights{
			ImportantPhrases: []string{"ghost phrase"},
			ImportantWords:   []string{"ghost word"},
		},
	}
	assert.Nil(t, projectEntityAnnotations("completely different text", "en", nilSeg))
}

// TestProjectEntityAnnotations_SkipsVisualSubject certifies the
// VISUAL_SUBJECT skip: a visual subject (e.g. the literal "PERSON" emitted by
// the Ollama extractor's EntitaSenzaTesto surface) is not a spoken entity and
// must never be projected into Annotations. Otherwise the entity timeline WORD
// gate would demand that the narration speaks "PERSON" verbatim and fail.
func TestProjectEntityAnnotations_SkipsVisualSubject(t *testing.T) {
	text := "L'attrice condivide una riflessione personale sul suo percorso."
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-0",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "PERSON", Type: "VISUAL_SUBJECT", Confidence: 0.92},
			},
		},
	}

	ann := projectEntityAnnotations(text, "it", seg)

	// The VISUAL_SUBJECT entity must be skipped entirely: no annotation is
	// produced for a segment whose only entity is a visual subject.
	require.Nil(t, ann, "VISUAL_SUBJECT entity must not produce annotations")
}

// TestNormalizeEntityAnnotationType_Golden02Vocabulary certifies the GOLDEN 02
// entity/type vocabulary maps onto the canonical annotation kinds
// (PERSON / company→ORG / MONEY / DATE / NUMBER / place→GPE), and that an
// unknown type fails safe to CONCEPT (never dropped, never mis-tagged).
func TestNormalizeEntityAnnotationType_Golden02Vocabulary(t *testing.T) {
	want := map[string]string{
		"PERSON":       "PERSON",
		"ORG":          "ORG",
		"ORGANIZATION": "ORG",
		"COMPANY":      "ORG",
		"CORP":         "ORG",
		"MONEY":        "MONEY",
		"DATE":         "DATE",
		"NUMBER":       "NUMBER",
		"NUM":          "NUMBER",
		"CARDINAL":     "CARDINAL",
		"PERCENT":      "PERCENT",
		"LOCATION":     "GPE",
		"GPE":          "GPE",
		"CITY":         "GPE",
	}
	for raw, expected := range want {
		if got := normalizeEntityAnnotationType(raw); got != expected {
			t.Errorf("normalizeEntityAnnotationType(%q) = %q, want %q", raw, got, expected)
		}
	}
	if got := normalizeEntityAnnotationType("FLYING_THING"); got != "CONCEPT" {
		t.Errorf("unknown type = %q, want CONCEPT (fail-safe)", got)
	}
}

// TestProjectSceneEntityResult_ClassifiesAndKeepsExplicitEmpty certifies the
// canonical per-scene EntityResult projection: PERSON → persons,
// LOCATION/PLACE/COUNTRY/CITY → places, every other type → concepts, and a
// scene that legitimately has no entities keeps an explicit empty result
// (entities=[]) — an entity is never invented.
func TestProjectSceneEntityResult_ClassifiesAndKeepsExplicitEmpty(t *testing.T) {
	seg := scriptpkg.VidRushSegmentResult{
		SegmentID: "seg-0",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Dwayne Johnson", Type: "PERSON", Confidence: 0.95},
				{Value: "Miami", Type: "LOCATION", Confidence: 0.9},
				{Value: "wrestling", Type: "CONCEPT", Confidence: 0.8},
			},
			ImportantPhrases: []string{"phrase one"},
			ImportantWords:   []string{"word one"},
		},
	}

	res := projectSceneEntityResult(seg)
	require.NotNil(t, res)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Dwayne Johnson", Type: "PERSON", Score: 0.95}}, res.Persons)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Miami", Type: "LOCATION", Score: 0.9}}, res.Places)
	assert.Equal(t, []scriptpkg.Entity{{Value: "wrestling", Type: "CONCEPT", Score: 0.8}}, res.Concepts)
	assert.Equal(t, []string{"phrase one"}, res.ImportantPhrases)
	assert.Equal(t, []string{"word one"}, res.ImportantWords)
	assert.True(t, entityResultHasValues(res))

	// A scene that legitimately has no entities keeps an explicit empty result
	// (entities=[]) with entity_overlay_required=false — never a fabricated
	// entity.
	empty := projectSceneEntityResult(scriptpkg.VidRushSegmentResult{})
	require.NotNil(t, empty, "empty scene must keep an explicit empty result, not nil")
	assert.Empty(t, empty.Persons)
	assert.Empty(t, empty.Places)
	assert.Empty(t, empty.Concepts)
	assert.False(t, entityResultHasValues(empty))
}

// TestApplySegmentEntityResults_MatchesScenesAndEmptyCase certifies the runner
// wiring of the per-scene EntityResult projection: every scene with a matching
// segment gets its typed Entities + entity_overlay_required, and a scene with
// no entities is explicit (entities=[] + false).
func TestApplySegmentEntityResults_MatchesScenesAndEmptyCase(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Dwayne Johnson in Miami."}},
			{ID: "scene-1", Index: 1, Text: map[Language]string{"en": "A plain sentence with no names."}},
		},
	}
	segments := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-0", Position: 0, Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{
				{Value: "Dwayne Johnson", Type: "PERSON", Confidence: 0.95},
				{Value: "Miami", Type: "LOCATION", Confidence: 0.9},
			},
		}},
		{SceneID: "scene-1", Position: 1, Insights: scriptpkg.SegmentInsights{}},
	}

	applySegmentEntityResults(result, segments)

	require.NotNil(t, result.Scenes[0].Entities)
	assert.Len(t, result.Scenes[0].Entities.Persons, 1)
	assert.Equal(t, "Dwayne Johnson", result.Scenes[0].Entities.Persons[0].Value)
	assert.Len(t, result.Scenes[0].Entities.Places, 1)
	assert.True(t, result.Scenes[0].EntityOverlayRequired)

	// scene-1 has no entities → explicit empty result + entity_overlay_required=false.
	require.NotNil(t, result.Scenes[1].Entities, "empty scene must keep an explicit empty result")
	assert.Empty(t, result.Scenes[1].Entities.Persons)
	assert.Empty(t, result.Scenes[1].Entities.Places)
	assert.Empty(t, result.Scenes[1].Entities.Concepts)
	assert.False(t, result.Scenes[1].EntityOverlayRequired)
}

// TestApplySegmentEntityResults_DoesNotLeakAcrossScenes certifies the negative
// case for the per-scene EntityResult surface: an entity extracted for one
// scene must never appear in another scene's Entities, and vice versa.
func TestApplySegmentEntityResults_DoesNotLeakAcrossScenes(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-dwayne", Index: 0, Text: map[Language]string{"en": "Dwayne Johnson in Los Angeles."}},
			{ID: "scene-serena", Index: 1, Text: map[Language]string{"en": "Serena Williams in New York."}},
		},
	}
	segments := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-dwayne", Position: 0, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Dwayne Johnson", Type: "PERSON", Confidence: 0.95},
			{Value: "Los Angeles", Type: "LOCATION", Confidence: 0.9},
		}}},
		{SceneID: "scene-serena", Position: 1, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Serena Williams", Type: "PERSON", Confidence: 0.95},
			{Value: "New York", Type: "LOCATION", Confidence: 0.9},
		}}},
	}

	applySegmentEntityResults(result, segments)

	for _, e := range result.Scenes[0].Entities.Persons {
		assert.NotEqual(t, "Serena Williams", e.Value)
	}
	for _, e := range result.Scenes[1].Entities.Persons {
		assert.NotEqual(t, "Dwayne Johnson", e.Value)
	}
	assert.Len(t, result.Scenes[0].Entities.Persons, 1)
	assert.Len(t, result.Scenes[1].Entities.Persons, 1)
}

// TestApplySegmentEntityAnnotations_DoesNotLeakAcrossScenes certifies the
// negative case from the certification spec: an entity extracted for one scene
// must never appear in another scene's annotations, and vice versa.
func TestApplySegmentEntityAnnotations_DoesNotLeakAcrossScenes(t *testing.T) {
	result := &GenerateResult{
		Scenes: []Scene{
			{ID: "scene-dwayne", Index: 0, Text: map[Language]string{"en": "Dwayne Johnson trained in Los Angeles."}},
			{ID: "scene-serena", Index: 1, Text: map[Language]string{"en": "Serena Williams appears in New York."}},
		},
	}
	segments := []scriptpkg.VidRushSegmentResult{
		{SceneID: "scene-dwayne", Position: 0, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Dwayne Johnson", Type: "PERSON", Confidence: 0.95},
			{Value: "Los Angeles", Type: "LOCATION", Confidence: 0.9},
		}}},
		{SceneID: "scene-serena", Position: 1, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Serena Williams", Type: "PERSON", Confidence: 0.95},
			{Value: "New York", Type: "LOCATION", Confidence: 0.9},
		}}},
	}

	applySegmentEntityAnnotations(result, "en", segments)

	dwayne := result.Scenes[0].Annotations
	serena := result.Scenes[1].Annotations
	require.NotNil(t, dwayne)
	require.NotNil(t, serena)

	// Dwayne's entities must never leak into Serena's annotations, and vice versa.
	for _, e := range append(serena.PrimaryEntities, serena.SecondaryEntities...) {
		assert.NotEqual(t, "Dwayne Johnson", e.CanonicalName)
		assert.NotEqual(t, "Los Angeles", e.CanonicalName)
	}
	for _, e := range append(dwayne.PrimaryEntities, dwayne.SecondaryEntities...) {
		assert.NotEqual(t, "Serena Williams", e.CanonicalName)
		assert.NotEqual(t, "New York", e.CanonicalName)
	}

	// Each scene keeps exactly its own entities.
	require.Len(t, dwayne.PrimaryEntities, 2)
	assert.Equal(t, "Dwayne Johnson", dwayne.PrimaryEntities[0].CanonicalName)
	require.Len(t, serena.PrimaryEntities, 2)
	assert.Equal(t, "Serena Williams", serena.PrimaryEntities[0].CanonicalName)
}

package rustexec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const siGolden = `{"schema_version":"scene_intelligence.v1","analyzer_version":"scene_intelligence.v1","relation_registry_version":"relation_registry.v1","scenes":[{"scene_id":"scene-01","density":{"scene_id":"scene-01","score":0.2,"level":"low","new_entities":1,"numeric_claims":0,"new_concepts":2,"suggested_visual_budget":"standard"},"comparisons":[{"scene_id":"scene-01","kind":"comparison","subjects":["ford","toyota"],"dimension":"general","relation":"contrasts_with","evidence_sentence_ids":[0],"candidate_visual":"split_comparison"}],"causal":[],"temporal_events":[{"event_id":"event-0000","text_span":"Nel 2020 i costi aumentarono.","temporal_expression":"2020","normalized_year":2020,"certainty":0.95,"narration_index":0}],"relations":[]}]}`

func TestDecodeSIGoldenManifest(t *testing.T) {
	m, err := DecodeSIManifest([]byte(siGolden))
	require.NoError(t, err)
	require.Len(t, m.Scenes, 1)
	assert.Equal(t, "scene-01", m.Scenes[0].SceneID)
	require.Len(t, m.Scenes[0].Comparisons, 1)
	assert.Empty(t, m.Scenes[0].Comparisons[0].Values, "golden qualitative comparison carries no values")
	require.Len(t, m.Scenes[0].TemporalEvents, 1)
	assert.Equal(t, 2020, *m.Scenes[0].TemporalEvents[0].NormalizedYear)
}

func TestDecodeSIRejectsQualitativeWithValues(t *testing.T) {
	bad := strings.Replace(siGolden, `"candidate_visual":"split_comparison"`, `"candidate_visual":"split_comparison","values":[42]`, 1)
	_, err := DecodeSIManifest([]byte(bad))
	assert.Error(t, err, "qualitative comparison must not carry values")
}

func TestDecodeSIRejectsUnknownSchema(t *testing.T) {
	_, err := DecodeSIManifest([]byte(`{"schema_version":"scene_intelligence.v99"}`))
	assert.Error(t, err)
}

func TestDecodeSINewFields(t *testing.T) {
	raw := `{"schema_version":"scene_intelligence.v1","analyzer_version":"scene_intelligence.v1","relation_registry_version":"relation_registry.v1","scenes":[{"scene_id":"s","density":{"scene_id":"s","score":0.3,"level":"low","suggested_visual_budget":"standard"},"comparisons":[{"scene_id":"s","kind":"comparison","subjects":["ford","toyota"],"dimension":"general","relation":"contrasts_with","evidence_sentence_ids":[0],"candidate_visual":"split_comparison","negated":true}],"causal":[{"kind":"causal_relation","cause":"c","effect":"e","relation":"causes","evidence_sentence_ids":[1],"extraction_confidence":0.6,"attribution":"Secondo gli analisti","hypothetical":true}],"temporal_events":[{"event_id":"event-0000","text_span":"2020–2023","temporal_expression":"2020–2023","normalized_year":2020,"normalized_end_year":2023,"certainty":0.9,"narration_index":0}]}]}`
	m, err := DecodeSIManifest([]byte(raw))
	require.NoError(t, err)
	assert.True(t, m.Scenes[0].Comparisons[0].Negated)
	assert.True(t, m.Scenes[0].Causal[0].Hypothetical)
	require.NotNil(t, m.Scenes[0].Causal[0].Attribution)
	require.NotNil(t, m.Scenes[0].TemporalEvents[0].NormalizedEndYear)
	assert.Equal(t, 2023, *m.Scenes[0].TemporalEvents[0].NormalizedEndYear)
}

func TestDecodeSIRejectsUnknownRelation(t *testing.T) {
	// X09 (bridge half): detector output outside the registry is rejected
	// before admission or render.
	bad := `{"schema_version":"scene_intelligence.v1","analyzer_version":"scene_intelligence.v1","relation_registry_version":"relation_registry.v1","scenes":[{"scene_id":"s","density":{"scene_id":"s","score":0.2,"level":"low","suggested_visual_budget":"standard"},"relations":[{"subject":"a","object":"b","relation":"best_friends_forever","evidence_sentence_ids":[0]}]}]}`
	_, err := DecodeSIManifest([]byte(bad))
	assert.Error(t, err)
}

func TestDecodeSIFailSoftContract(t *testing.T) {
	// X07: an unavailable/corrupt Rust worker is a typed error, never a
	// panic — callers continue without Scene Intelligence.
	for _, raw := range []string{"", "not json", `{"schema_version":"x"}`, "null"} {
		assert.NotPanics(t, func() {
			_, _ = DecodeSIManifest([]byte(raw))
		})
		_, err := DecodeSIManifest([]byte(raw))
		assert.Error(t, err)
	}
}

func TestEncodeSIRequestCanonical(t *testing.T) {
	a, err := EncodeSIAnalysisRequest([]SISceneRequest{
		{SceneID: "scene-02", Text: "B", VideoStartMS: 0, VideoEndMS: 1000, Entities: []string{"b", "a"}},
		{SceneID: "scene-01", Text: "A", VideoStartMS: 0, VideoEndMS: 1000},
	})
	require.NoError(t, err)
	b, err := EncodeSIAnalysisRequest([]SISceneRequest{
		{SceneID: "scene-01", Text: "A", VideoStartMS: 0, VideoEndMS: 1000},
		{SceneID: "scene-02", Text: "B", VideoStartMS: 0, VideoEndMS: 1000, Entities: []string{"a", "b"}},
	})
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b), "request encoding is canonical regardless of input order")
	_, err = EncodeSIAnalysisRequest(nil)
	assert.Error(t, err)
	_, err = EncodeSIAnalysisRequest([]SISceneRequest{{SceneID: "s", Text: "x", VideoStartMS: 5, VideoEndMS: 5}})
	assert.Error(t, err, "event time and video time windows must be valid")
}

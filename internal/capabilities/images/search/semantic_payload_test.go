package images

import (
	"testing"
)

func TestSemanticPayloadToMap(t *testing.T) {
	score := 0.95
	payload := &SemanticPayload{
		AssetID:             "asset-1",
		PromptOriginal:      "A cat",
		SemanticDescription: "A domestic cat",
		SearchText:          "cat domestic",
		Subjects:            []string{"cat"},
		Tags:                []string{"animal", "pet"},
		Categories:          []string{"mammals"},
		Mood:                []string{"calm"},
		Style:               []string{"realistic"},
		ConceptTags:         []string{"feline"},
		VisualObjects:       []string{"cat", "sofa"},
		EmotionalTone:       []string{"peaceful"},
		RetrievalScore:      &score,
	}

	m := semanticPayloadToMap(payload)
	if m == nil {
		t.Fatal("expected non-nil map")
	}

	assertString(t, m, "asset_id", "asset-1")
	assertString(t, m, "prompt_original", "A cat")
	assertString(t, m, "semantic_description", "A domestic cat")
	assertString(t, m, "search_text", "cat domestic")
	assertStringSlice(t, m, "tags", []string{"animal", "pet"})
	assertStringSlice(t, m, "categories", []string{"mammals"})
	assertStringSlice(t, m, "mood", []string{"calm"})
	assertStringSlice(t, m, "style", []string{"realistic"})
	assertStringSlice(t, m, "concept_tags", []string{"feline"})
	assertStringSlice(t, m, "visual_objects", []string{"cat", "sofa"})
	assertStringSlice(t, m, "emotional_tone", []string{"peaceful"})

	if got, ok := m["retrieval_score"].(float64); !ok || got != 0.95 {
		t.Errorf("retrieval_score = %v, want 0.95", m["retrieval_score"])
	}
}

func TestSemanticPayloadToMap_Nil(t *testing.T) {
	if got := semanticPayloadToMap(nil); got != nil {
		t.Fatalf("expected nil for nil payload, got %v", got)
	}
}

func assertString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, ok := m[key].(string)
	if !ok || got != want {
		t.Errorf("%s = %v, want %q", key, m[key], want)
	}
}

func assertStringSlice(t *testing.T, m map[string]any, key string, want []string) {
	t.Helper()
	v, ok := m[key].([]string)
	if !ok {
		t.Errorf("%s type = %T, want []string", key, m[key])
		return
	}
	if len(v) != len(want) {
		t.Errorf("%s length = %d, want %d", key, len(v), len(want))
		return
	}
	for i := range v {
		if v[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", key, i, v[i], want[i])
		}
	}
}

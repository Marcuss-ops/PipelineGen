package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSIRunner answers like the Rust worker for canned inputs and counts
// round-trips so the batch test pins ONE call for N scenes.
type fakeSIRunner struct {
	calls int
	last  []byte
}

func (f *fakeSIRunner) Run(_ context.Context, _ string, input []byte, _ int64) ([]byte, []byte, error) {
	f.calls++
	f.last = append([]byte(nil), input...)
	var req map[string]any
	if err := json.Unmarshal(input, &req); err != nil {
		return []byte(`{"ok":false,"error":"bad json"}`), nil, nil
	}
	switch req["operation"] {
	case "split_sentences":
		text, _ := req["text"].(string)
		scene, _ := req["scene_id"].(string)
		// Single-sentence echo with exact byte span.
		out, _ := json.Marshal(map[string]any{"ok": true, "sentences": []map[string]any{
			{"text": text, "start_byte": 0, "end_byte": len(text), "scene_id": scene},
		}})
		return append(out, '\n'), nil, nil
	default:
		manifest := map[string]any{
			"schema_version":            "scene_intelligence.v1",
			"analyzer_version":          "scene_intelligence.v1",
			"relation_registry_version": "relation_registry.v1",
			"scenes": []map[string]any{
				{"scene_id": "scene-01", "density": map[string]any{
					"scene_id": "scene-01", "score": 0.2, "level": "low",
					"suggested_visual_budget": "standard",
				}},
			},
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "manifest": manifest})
		return append(out, '\n'), nil, nil
	}
}

func TestSIAnalyzer_BatchIsOneRoundTrip(t *testing.T) {
	fake := &fakeSIRunner{}
	analyzer := NewSIAnalyzer("bin/scene_intelligence", fake)
	scenes := []SISceneRequest{
		{SceneID: "scene-02", Text: "Secondo testo.", VideoStartMS: 1000, VideoEndMS: 2000, Entities: []string{"b"}},
		{SceneID: "scene-01", Text: "Primo testo.", VideoStartMS: 0, VideoEndMS: 1000},
	}
	manifest, err := analyzer.AnalyzeBatch(context.Background(), scenes)
	require.NoError(t, err)
	require.Len(t, manifest.Scenes, 1)
	assert.Equal(t, 1, fake.calls, "N scenes must cost ONE worker round-trip")
	var sent map[string]any
	require.NoError(t, json.Unmarshal(fake.last, &sent))
	assert.Equal(t, "analyze", sent["operation"])
	assert.Len(t, sent["scenes"], 2, "both scenes in the single request")
	assert.Len(t, sent["entities_per_scene"], 2)
}

func TestSIAnalyzer_EmptyBatchFailsClosed(t *testing.T) {
	analyzer := NewSIAnalyzer("bin/scene_intelligence", &fakeSIRunner{})
	_, err := analyzer.AnalyzeBatch(context.Background(), nil)
	assert.Error(t, err)
}

func TestSIAnalyzer_UnconfiguredWorkerFailsClosed(t *testing.T) {
	analyzer := &SIAnalyzer{}
	_, err := analyzer.AnalyzeBatch(context.Background(), []SISceneRequest{
		{SceneID: "s", Text: "Ciao.", VideoStartMS: 0, VideoEndMS: 1000},
	})
	assert.Error(t, err)
}

func TestSIAnalyzer_SplitVerifiesByteOffsets(t *testing.T) {
	fake := &fakeSIRunner{}
	analyzer := NewSIAnalyzer("bin/scene_intelligence", fake)
	sentences, err := analyzer.SplitSentencesViaWorker(context.Background(), "Ciao mondo.", "s-01")
	require.NoError(t, err)
	require.Len(t, sentences, 1)
	assert.Equal(t, "Ciao mondo.", sentences[0])
}

func TestSIAnalyzer_SplitRejectsCorruptOffsets(t *testing.T) {
	runner := &corruptSplitRunner{}
	analyzer := NewSIAnalyzer("bin/scene_intelligence", runner)
	_, err := analyzer.SplitSentencesViaWorker(context.Background(), "Ciao mondo.", "s-01")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "byte offsets"))
}

type corruptSplitRunner struct{}

func (c *corruptSplitRunner) Run(_ context.Context, _ string, _ []byte, _ int64) ([]byte, []byte, error) {
	return []byte(`{"ok":true,"sentences":[{"text":"MONDO","start_byte":5,"end_byte":30,"scene_id":"s-01"}]}`), nil, nil
}

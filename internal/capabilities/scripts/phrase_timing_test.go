package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// speechTimingForWords builds a valid canonical timing artifact with one
// 100ms word per input token, so every span assertion is exact.
func speechTimingForWords(texts []string) audio.SpeechTimingArtifact {
	words := make([]audio.SpeechWordTiming, len(texts))
	for i, text := range texts {
		words[i] = audio.SpeechWordTiming{
			Index:   i,
			Text:    text,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	return audio.SpeechTimingArtifact{
		Version:      audio.SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: audio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "text-hash",
		AudioSHA256:  "audio-hash",
		DurationUS:   int64(len(texts)) * 100_000,
		Words:        words,
	}
}

func TestCompilePhraseTimings_PersistsLocalAndGlobalSpans(t *testing.T) {
	scenes := []ResolvedScene{
		{ID: "scene-0", Index: 0, TimelineStartUS: 0},
		{ID: "scene-1", Index: 1, TimelineStartUS: 8_200_000},
	}
	timing := speechTimingForWords([]string{"Jackie", "Chan", "grew", "up", "in", "Hong", "Kong"})
	sources := map[string]PhraseTimingSource{
		"scene-1": {Timing: timing, Phrases: []string{"Jackie Chan", "grew up"}},
	}

	got, err := CompilePhraseTimings(scenes, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d phrase timings, want 2", len(got))
	}

	// "Jackie Chan": words 0..1, local [0, 200_000).
	first := got[0]
	if first.SceneIndex != 1 || first.PhraseIndex != 0 {
		t.Fatalf("first scene/phrase index = %d/%d, want 1/0", first.SceneIndex, first.PhraseIndex)
	}
	if first.LocalStartUS != 0 || first.LocalEndUS != 200_000 {
		t.Fatalf("first local span = [%d,%d), want [0,200000)", first.LocalStartUS, first.LocalEndUS)
	}
	if first.TimelineStartUS != 8_200_000 {
		t.Fatalf("first timeline start = %d, want 8200000", first.TimelineStartUS)
	}
	if first.GlobalStartUS != 8_200_000 || first.GlobalEndUS != 8_400_000 {
		t.Fatalf("first global span = [%d,%d), want [8200000,8400000)", first.GlobalStartUS, first.GlobalEndUS)
	}

	// "grew up": words 2..3, local [200_000, 400_000).
	second := got[1]
	if second.LocalStartUS != 200_000 || second.LocalEndUS != 400_000 {
		t.Fatalf("second local span = [%d,%d), want [200000,400000)", second.LocalStartUS, second.LocalEndUS)
	}
	if second.GlobalStartUS != 8_400_000 || second.GlobalEndUS != 8_600_000 {
		t.Fatalf("second global span = [%d,%d), want [8400000,8600000)", second.GlobalStartUS, second.GlobalEndUS)
	}
	for _, p := range got {
		if err := p.Validate(); err != nil {
			t.Fatalf("projection invalid: %v", err)
		}
	}
}

// TestCompilePhraseTimings_SerializesOnGenerateResult pins the persistence
// surface: the projection survives a JSON round-trip on GenerateResult and
// stays valid.
func TestCompilePhraseTimings_SerializesOnGenerateResult(t *testing.T) {
	res := &GenerateResult{
		PhraseTimings: []audio.PhraseTiming{{
			SceneIndex:      0,
			PhraseIndex:     0,
			Text:            "Jackie Chan",
			WordStart:       0,
			WordEnd:         1,
			LocalStartUS:    0,
			LocalEndUS:      200_000,
			TimelineStartUS: 8_200_000,
			GlobalStartUS:   8_200_000,
			GlobalEndUS:     8_400_000,
		}},
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back GenerateResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.PhraseTimings) != 1 {
		t.Fatalf("round-trip lost phrase timings: %s", raw)
	}
	if err := back.PhraseTimings[0].Validate(); err != nil {
		t.Fatalf("round-tripped phrase timing invalid: %v", err)
	}
}

func TestCompilePhraseTimings_MissingPhraseFailsClosed(t *testing.T) {
	scenes := []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0}}
	timing := speechTimingForWords([]string{"Jackie", "Chan"})
	sources := map[string]PhraseTimingSource{
		"scene-0": {Timing: timing, Phrases: []string{"Mussolini"}},
	}
	if _, err := CompilePhraseTimings(scenes, sources); err == nil {
		t.Fatal("expected missing phrase to fail closed")
	}
}

func TestCompilePhraseTimings_UnknownSceneSourceFailsClosed(t *testing.T) {
	scenes := []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0}}
	sources := map[string]PhraseTimingSource{
		"scene-1": {Timing: speechTimingForWords([]string{"Jackie"}), Phrases: []string{"Jackie"}},
	}
	if _, err := CompilePhraseTimings(scenes, sources); err == nil {
		t.Fatal("expected unknown scene source to fail closed")
	}
}

// TestCompileSceneSpeechTimings_BundlesPerSceneWordsAndPhrases pins the
// scene-level projection: one SceneSpeechTiming per scene with a source,
// bundling the scene's word boundaries with its derived phrase spans whose
// global coordinates come from the scene's canonical timeline offset.
func TestCompileSceneSpeechTimings_BundlesPerSceneWordsAndPhrases(t *testing.T) {
	scenes := []ResolvedScene{
		{ID: "scene-0", Index: 0, TimelineStartUS: 0},
		{ID: "scene-1", Index: 1, TimelineStartUS: 8_200_000},
	}
	timing := speechTimingForWords([]string{"Jackie", "Chan", "grew", "up", "in", "Hong", "Kong"})
	sources := map[string]PhraseTimingSource{
		"scene-1": {Timing: timing, Phrases: []string{"Jackie Chan", "grew up"}, VoiceoverAssetID: "vo-1"},
	}

	got, err := CompileSceneSpeechTimings(scenes, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scene speech timings, want 1", len(got))
	}
	st := got[0]
	if st.SceneID != "scene-1" || st.VoiceoverAssetID != "vo-1" {
		t.Fatalf("scene id/asset = %q/%q", st.SceneID, st.VoiceoverAssetID)
	}
	if st.LocalDurationUS != 700_000 {
		t.Fatalf("local duration = %d, want 700000", st.LocalDurationUS)
	}
	if len(st.Words) != 7 {
		t.Fatalf("words = %d, want 7", len(st.Words))
	}
	if len(st.Phrases) != 2 {
		t.Fatalf("phrases = %d, want 2", len(st.Phrases))
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("scene speech timing invalid: %v", err)
	}
	for _, p := range st.Phrases {
		if p.GlobalStartUS != 8_200_000+p.LocalStartUS || p.GlobalEndUS != 8_200_000+p.LocalEndUS {
			t.Fatalf("phrase global span drifted from timeline offset: %+v", p)
		}
	}
}

// TestCompileSceneSpeechTimings_SerializesOnGenerateResult pins the
// persistence surface: the scene-level projection survives a JSON round-trip
// on GenerateResult and stays valid.
func TestCompileSceneSpeechTimings_SerializesOnGenerateResult(t *testing.T) {
	res := &GenerateResult{
		SceneSpeechTimings: []audio.SceneSpeechTiming{{
			SceneID:          "scene-0",
			VoiceoverAssetID: "vo-0",
			LocalDurationUS:  200_000,
			Words: []audio.SpeechWordTiming{
				{Index: 0, Text: "Jackie", StartUS: 0, EndUS: 100_000},
				{Index: 1, Text: "Chan", StartUS: 100_000, EndUS: 200_000},
			},
			Phrases: []audio.PhraseTiming{{
				SceneIndex:      0,
				PhraseIndex:     0,
				Text:            "Jackie Chan",
				WordStart:       0,
				WordEnd:         1,
				LocalStartUS:    0,
				LocalEndUS:      200_000,
				TimelineStartUS: 0,
				GlobalStartUS:   0,
				GlobalEndUS:     200_000,
			}},
		}},
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back GenerateResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.SceneSpeechTimings) != 1 {
		t.Fatalf("round-trip lost scene speech timings: %s", raw)
	}
	if err := back.SceneSpeechTimings[0].Validate(); err != nil {
		t.Fatalf("round-tripped scene speech timing invalid: %v", err)
	}
}

// TestCompileSceneSpeechTimings_UnknownSceneSourceFailsClosed mirrors the flat
// projection contract: a source referencing an unknown scene aborts instead of
// silently dropping it.
func TestCompileSceneSpeechTimings_UnknownSceneSourceFailsClosed(t *testing.T) {
	scenes := []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0}}
	sources := map[string]PhraseTimingSource{
		"scene-1": {Timing: speechTimingForWords([]string{"Jackie"}), Phrases: []string{"Jackie"}},
	}
	if _, err := CompileSceneSpeechTimings(scenes, sources); err == nil {
		t.Fatal("expected unknown scene source to fail closed")
	}
}

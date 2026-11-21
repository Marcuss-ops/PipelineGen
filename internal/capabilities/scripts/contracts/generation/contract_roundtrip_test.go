package generation

import (
	"encoding/json"
	"reflect"
	"testing"

	scenecontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRequestRoundTripPreservesPhaseFields(t *testing.T) {
	request := Request{
		TranslationProvider: "deepl", TranslationModel: "quality", TranslationConcurrency: 3,
		Model: "llama", Tone: "documentary", Style: "cinematic",
		ChannelID: "channel-1", SourceLanguage: scenecontract.Language("it"),
		Languages: []scenecontract.Language{"en", "es"},
		Title:     "Titolo", Project: "project", OutputName: "out.mp4",
		EditorialDensity: "balanced", MaxPhraseOverlays: 7, MaxOverlaysPerScene: 2,
		EnableCharts: true, HeavyPhrasePriority: 0.8,
		ScriptParams: scriptpkg.ScriptSpec{},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, decoded) {
		t.Fatalf("request round-trip mismatch:\n got %+v\nwant %+v", decoded, request)
	}
}

func TestResultRoundTripPreservesEditorialFields(t *testing.T) {
	result := Result{
		Output: Output{Text: "story", WordCount: 1},
		Title:  "Titolo", SourceLanguage: scenecontract.Language("it"), OutputName: "out.mp4",
		WordCount: 1, VoiceoverGroup: "group", Summary: "summary", BulletPoints: []string{"b"},
		BinarySHA256: "abc", RustRelease: "r1",
		TranslationMetrics: TranslationMetrics{Calls: 2, Concurrency: 1, WallMS: 10},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, decoded) {
		t.Fatalf("result round-trip mismatch:\n got %+v\nwant %+v", decoded, result)
	}
}

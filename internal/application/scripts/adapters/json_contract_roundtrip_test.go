package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestPostProcessResult_JSONRoundTripExcludesInternalState(t *testing.T) {
	original := PostProcessResult{
		DocID:            "doc-1",
		DocLink:          "https://docs.example/doc-1",
		ScriptID:         42,
		Changed:          true,
		DurationMs:       17,
		TranslatedText:   "testo tradotto",
		Warnings:         []string{"soft warning"},
		SpecSceneChanged: true,
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal PostProcessResult: %v", err)
	}
	wire := string(raw)
	for _, key := range []string{`"DocID"`, `"DocLink"`, `"ScriptID"`, `"changed"`, `"duration_ms"`, `"translated_text"`, `"warnings"`} {
		if !strings.Contains(wire, key) {
			t.Fatalf("expected JSON key %s in PostProcessResult: %s", key, wire)
		}
	}
	if strings.Contains(wire, "SpecSceneChanged") || strings.Contains(wire, "spec_scene_changed") {
		t.Fatalf("internal SpecSceneChanged leaked into JSON: %s", raw)
	}

	var decoded PostProcessResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal PostProcessResult: %v", err)
	}
	if decoded.DocID != original.DocID || decoded.DocLink != original.DocLink || decoded.ScriptID != original.ScriptID ||
		decoded.Changed != original.Changed || decoded.DurationMs != original.DurationMs ||
		decoded.TranslatedText != original.TranslatedText || len(decoded.Warnings) != 1 {
		t.Fatalf("round-trip mismatch: original=%#v decoded=%#v", original, decoded)
	}
	if decoded.SpecSceneChanged {
		t.Fatal("internal SpecSceneChanged must not be restored from JSON")
	}
}

func TestPipelineResult_JSONRoundTripExcludesInternalState(t *testing.T) {
	original := PipelineResult{
		DocID:             "doc-2",
		ScriptID:          99,
		StageDurations:    map[string]int64{"translation": 23},
		TranslatedText:    "translated pipeline text",
		EffectiveLanguage: "it",
		Warnings:          []string{"pipeline warning"},
		SpecSceneChanged:  true,
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal PipelineResult: %v", err)
	}
	wire := string(raw)
	for _, key := range []string{`"DocID"`, `"ScriptID"`, `"stage_durations"`, `"translated_text"`, `"effective_language"`, `"warnings"`} {
		if !strings.Contains(wire, key) {
			t.Fatalf("expected JSON key %s in PipelineResult: %s", key, wire)
		}
	}
	if strings.Contains(wire, "SpecSceneChanged") || strings.Contains(wire, "spec_scene_changed") {
		t.Fatalf("internal SpecSceneChanged leaked into JSON: %s", raw)
	}

	var decoded PipelineResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal PipelineResult: %v", err)
	}
	if decoded.DocID != original.DocID || decoded.ScriptID != original.ScriptID ||
		decoded.StageDurations["translation"] != 23 || decoded.TranslatedText != original.TranslatedText ||
		decoded.EffectiveLanguage != original.EffectiveLanguage || len(decoded.Warnings) != 1 {
		t.Fatalf("round-trip mismatch: original=%#v decoded=%#v", original, decoded)
	}
	if decoded.SpecSceneChanged {
		t.Fatal("internal SpecSceneChanged must not be restored from JSON")
	}
}

func TestProcessInput_JSONRoundTripExcludesInternalState(t *testing.T) {
	original := ProcessInput{
		Text:              "input text",
		WordCount:         12,
		EffectiveLanguage: "en",
		StockEnabled:      scriptpkg.ToggleDefault,
		SpecSceneChanged:  true,
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal ProcessInput: %v", err)
	}
	if strings.Contains(string(raw), "SpecSceneChanged") || strings.Contains(string(raw), "spec_scene_changed") {
		t.Fatalf("internal SpecSceneChanged leaked into JSON: %s", raw)
	}

	var decoded ProcessInput
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal ProcessInput: %v", err)
	}
	if decoded.Text != original.Text || decoded.WordCount != original.WordCount || decoded.EffectiveLanguage != original.EffectiveLanguage {
		t.Fatalf("round-trip mismatch: original=%#v decoded=%#v", original, decoded)
	}
	if decoded.SpecSceneChanged {
		t.Fatal("internal SpecSceneChanged must not be restored from JSON")
	}
}

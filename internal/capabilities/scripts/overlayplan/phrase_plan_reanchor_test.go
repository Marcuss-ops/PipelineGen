package overlayplan

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// TestReanchorPhrasePlanItemsKeepsPlannerWindowWhenNotAnchored pins the replay
// contract: when the certified voiceover timing no longer contains the phrase
// surface (a cached plan replayed over a regenerated transcript), the item must
// keep its planner window with an explicit reason instead of failing the whole
// generation at COMPILING_AUDIO.
func TestReanchorPhrasePlanItemsKeepsPlannerWindowWhenNotAnchored(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID:         "short-phrase-editorial-focus-resolve__causa-ilary-effetto-causa",
		SceneID:    "scene-03",
		Kind:       "text_phrase",
		TemplateID: "IMPORTANT_PHRASE",
		Text:       "causa ilary effetto causa",
		StartUS:    1_000_000,
		DurationUS: 2_000_000,
	}}}
	result := &contract.GenerateResult{}
	result.SceneSpeechTimings = []capabilityaudio.SceneSpeechTiming{{
		SceneID: "scene-03",
		Words: []capabilityaudio.SpeechWordTiming{
			{Text: "un'altra", StartUS: 0, EndUS: 400_000},
			{Text: "frase", StartUS: 400_000, EndUS: 900_000},
		},
	}}

	if err := reanchorPhrasePlanItems(plan, result); err != nil {
		t.Fatalf("reanchorPhrasePlanItems() error = %v, want the planner window kept", err)
	}
	item := plan.Items[0]
	if item.StartUS != 1_000_000 || item.DurationUS != 2_000_000 {
		t.Fatalf("window = [%d,+%d], want the planner window [1000000,+2000000]", item.StartUS, item.DurationUS)
	}
	if got := item.Params["phrase_anchor_reason"]; got != "not_anchored" {
		t.Fatalf("phrase_anchor_reason = %v, want not_anchored", got)
	}
	if got := item.Params["phrase_anchor"]; got != "planner_window" {
		t.Fatalf("phrase_anchor = %v, want planner_window", got)
	}
}

// TestReanchorPhrasePlanItemsStillCutsToCertifiedWords keeps the primary
// behaviour intact: a phrase that IS present in the certified timing is
// re-cut to the spoken words, so degradation only applies to the unmatched
// case.
func TestReanchorPhrasePlanItemsStillCutsToCertifiedWords(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID:         "phrase-anchored",
		SceneID:    "scene-01",
		Kind:       "text_phrase",
		TemplateID: "IMPORTANT_PHRASE",
		Text:       "causa effetto",
		StartUS:    5_000_000,
		DurationUS: 1_000_000,
	}}}
	result := &contract.GenerateResult{}
	result.SceneSpeechTimings = []capabilityaudio.SceneSpeechTiming{{
		SceneID: "scene-01",
		Words: []capabilityaudio.SpeechWordTiming{
			{Text: "prima", StartUS: 0, EndUS: 200_000},
			{Text: "causa", StartUS: 200_000, EndUS: 450_000},
			{Text: "effetto", StartUS: 450_000, EndUS: 800_000},
		},
	}}

	if err := reanchorPhrasePlanItems(plan, result); err != nil {
		t.Fatalf("reanchorPhrasePlanItems() error = %v", err)
	}
	item := plan.Items[0]
	if item.StartUS != 200_000 || item.DurationUS != 600_000 {
		t.Fatalf("window = [%d,+%d], want [200000,+600000]", item.StartUS, item.DurationUS)
	}
	if _, degraded := item.Params["phrase_anchor_reason"]; degraded {
		t.Fatalf("anchored phrase must not carry a degradation reason: %+v", item.Params)
	}
}

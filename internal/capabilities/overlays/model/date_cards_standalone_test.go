package model

import "testing"

// Four nearby date cards must stay four standalone number items. Chaining
// them re-times every card sequentially and can push the last one past its
// scene, hiding it under the next scene's imagery.
func TestCoalesceDateCardsStayStandalone(t *testing.T) {
	items := []OverlayItem{
		{ID: "date-0", SceneID: "s1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", Text: "3 giugno 2007", StartUS: 1_600_000, DurationUS: 3_200_000, MotionID: "date_fade_rise", PresetID: "phrase_default"},
		{ID: "date-1", SceneID: "s1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", Text: "14 ottobre 2010", StartUS: 4_900_000, DurationUS: 3_200_000, MotionID: "date_timeline_tick", PresetID: "phrase_default"},
		{ID: "date-2", SceneID: "s1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", Text: "22 aprile 2014", StartUS: 8_600_000, DurationUS: 3_200_000, MotionID: "date_chronology_focus", PresetID: "phrase_default"},
		{ID: "date-3", SceneID: "s1", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", Text: "9 novembre 2019", StartUS: 12_600_000, DurationUS: 3_200_000, MotionID: "date_calendar_drop", PresetID: "phrase_default"},
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != len(items) {
		t.Fatalf("nearby date cards coalesced into %d items, want %d standalone: %+v", len(got), len(items), got)
	}
	for i, item := range got {
		if item.Kind != "number" || item.TemplateID != "TIMELINE_DATE_CARD" {
			t.Fatalf("item %d = kind %q template %q, want a standalone TIMELINE_DATE_CARD", i, item.Kind, item.TemplateID)
		}
		if item.Params["chain_segments"] != nil {
			t.Fatalf("item %d carries chain segments: %+v", i, item.Params)
		}
	}
}

// A non-date number card still chains: the exclusion is specific to the date
// template, not to the whole number lane.
func TestCoalesceMetricNumbersStillChain(t *testing.T) {
	items := []OverlayItem{
		{ID: "metric-0", SceneID: "s1", Kind: "number", TemplateID: "METRIC_STAT_CARD", Text: "42", StartUS: 0, DurationUS: 2_000_000, MotionID: "number_count_up", PresetID: "phrase_default"},
		{ID: "metric-1", SceneID: "s1", Kind: "number", TemplateID: "METRIC_STAT_CARD", Text: "18", StartUS: 2_500_000, DurationUS: 2_000_000, MotionID: "number_slide", PresetID: "phrase_default"},
	}
	got := CoalesceChainedOverlays(items, map[string]int{"chain_enabled": 1})
	if len(got) != 1 || got[0].Kind != KindPhraseChain {
		t.Fatalf("metric numbers = %+v, want one phrase_chain", got)
	}
}

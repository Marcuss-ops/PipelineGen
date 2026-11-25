package model

import "testing"

func TestNormalizeChainSegmentOrderSortsByStart(t *testing.T) {
	item := OverlayItem{
		ID:         "static__mixed-chain__01",
		Kind:       KindMixedChain,
		TemplateID: TemplateMixedChain,
		StartUS:    0,
		DurationUS: 5_000_000,
		StartMs:    0,
		EndMs:      5_000,
		Params: map[string]any{
			"chain_segments": []map[string]any{
				{"text": "later", "motion_id": "motion", "start_us": 2_000_000, "duration_us": 1_000_000},
				{"text": "earlier", "motion_id": "motion", "start_us": 1_000_000, "duration_us": 1_000_000},
			},
		},
	}

	if err := ValidateChainItem(item); err == nil {
		t.Fatal("ValidateChainItem accepted a segment list with decreasing start_us")
	}
	if !NormalizeChainSegmentOrder(&item) {
		t.Fatal("NormalizeChainSegmentOrder reported no change for an unordered chain")
	}
	if err := ValidateChainItem(item); err != nil {
		t.Fatalf("ValidateChainItem after normalization: %v", err)
	}
	segments, ok := item.Segments()
	if !ok || len(segments) != 2 {
		t.Fatalf("segments = %+v, ok=%v; want two", segments, ok)
	}
	if segments[0].Text != "earlier" || segments[1].Text != "later" {
		t.Fatalf("segment order = %q, %q; want earlier, later", segments[0].Text, segments[1].Text)
	}
	if segments[0].StartUS != 1_000_000 || segments[1].StartUS != 2_000_000 {
		t.Fatalf("segment starts = %d, %d; want 1s, 2s", segments[0].StartUS, segments[1].StartUS)
	}
}

func TestNormalizeChainSegmentOrderLeavesOrderedChainUntouched(t *testing.T) {
	item := OverlayItem{
		ID:         "ordered",
		Kind:       KindPhraseChain,
		TemplateID: TemplatePhraseChain,
		StartUS:    0,
		DurationUS: 5_000_000,
		StartMs:    0,
		EndMs:      5_000,
		Params: map[string]any{
			"chain_segments": []map[string]any{
				{"text": "first", "motion_id": "motion", "start_us": 0, "duration_us": 1_000_000},
				{"text": "second", "motion_id": "motion", "start_us": 1_500_000, "duration_us": 1_000_000},
			},
		},
	}
	if NormalizeChainSegmentOrder(&item) {
		t.Fatal("NormalizeChainSegmentOrder rewrote an already ordered chain")
	}
}

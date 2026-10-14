package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestValidateOverlayGroupLimitsRejectsUnknownAndNegative(t *testing.T) {
	if err := validateOverlayGroupLimits(map[string]int{"maps": 10, "long_phrases": 0}); err != nil {
		t.Fatalf("valid limits rejected: %v", err)
	}
	if err := validateOverlayGroupLimits(map[string]int{"videos": 1}); err == nil {
		t.Fatal("unknown group accepted")
	}
	if err := validateOverlayGroupLimits(map[string]int{"maps": -1}); err == nil {
		t.Fatal("negative cap accepted")
	}
}

func TestApplyOverlayGroupLimitsCapsAndDisables(t *testing.T) {
	// The short-phrase group is classified by the motion's presence in the
	// PUBLISHED exact-cardinality recipe table, not by its wire prefix: a
	// retired id ("short_phrase_fade") keeps the prefix and is deliberately
	// routed to the long-phrase group. Use a recipe id the catalog publishes.
	items := []capabilityoverlay.OverlayItem{
		{ID: "m1", Kind: "map", Map: &capabilityoverlay.MapOverlay{}},
		{ID: "m2", Kind: "map", Map: &capabilityoverlay.MapOverlay{}},
		{ID: "m3", Kind: "map", Map: &capabilityoverlay.MapOverlay{}},
		{ID: "s1", Kind: "text_phrase", MotionID: "short_phrase_editorial_quiet_zoom"},
		{ID: "l1", Kind: "text_phrase", MotionID: "slide_in"},
		{ID: "l2", Kind: "text_phrase", MotionID: "slide_in"},
	}
	got := applyOverlayGroupLimits(append([]capabilityoverlay.OverlayItem(nil), items...), map[string]int{
		"maps":          2,
		"short_phrases": 1,
		"long_phrases":  0,
	})
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.ID)
	}
	want := []string{"m1", "m2", "s1"}
	if len(ids) != len(want) {
		t.Fatalf("kept %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("kept %v, want %v", ids, want)
		}
	}
}

func TestApplyOverlayGroupLimitsAbsentKeyKeepsDefault(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "l1", Kind: "text_phrase", MotionID: "slide_in"},
		{ID: "l2", Kind: "text_phrase", MotionID: "slide_in"},
	}
	got := applyOverlayGroupLimits(append([]capabilityoverlay.OverlayItem(nil), items...), map[string]int{"maps": 0})
	if len(got) != 2 {
		t.Fatalf("absent long_phrases key must keep all items, got %d", len(got))
	}
}

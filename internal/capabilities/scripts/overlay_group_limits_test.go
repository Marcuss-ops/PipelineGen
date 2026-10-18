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
	// Phrase groups follow spoken word count, independently of the animation
	// selected for the overlay.
	items := []capabilityoverlay.OverlayItem{
		{ID: "m1", Kind: "map", Map: &capabilityoverlay.MapOverlay{}},
		{ID: "m2", Kind: "map", Map: &capabilityoverlay.MapOverlay{}},
		{ID: "m3", Kind: "map", Map: &capabilityoverlay.MapOverlay{}},
		{ID: "s1", Kind: "text_phrase", Text: "two word phrase", MotionID: "phrase_apple_clean_07_slide_up_soft"},
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

func TestEffectivePhraseOverlayCeilingAllowsOnePhraseGroupToRaiseDefault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits map[string]int
	}{
		{name: "short only", limits: map[string]int{"short_phrases": 8}},
		{name: "long only", limits: map[string]int{"long_phrases": 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectivePhraseOverlayCeiling(0, tc.limits); got != 8 {
				t.Fatalf("effective phrase ceiling = %d, want 8", got)
			}
		})
	}
	for _, group := range []string{"short_phrases", "long_phrases"} {
		if got := effectivePhraseOverlayCeiling(0, map[string]int{group: 0}); got != capabilityoverlay.MaxPhraseOverlaysPerRun {
			t.Fatalf("explicit zero %s cap gives phrase ceiling %d, want default %d for the uncapped group", group, got, capabilityoverlay.MaxPhraseOverlaysPerRun)
		}
	}
}

func TestEffectiveMapOverlayCeilingHonorsRequestAndHardCap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested int
		limits    map[string]int
		want      int
	}{
		{name: "default bounded to three", want: 3},
		{name: "request lowers ceiling", requested: 2, want: 2},
		{name: "group lowers ceiling", limits: map[string]int{"maps": 1}, want: 1},
		{name: "request and group choose lower", requested: 2, limits: map[string]int{"maps": 1}, want: 1},
		{name: "oversized request remains bounded", requested: 20, limits: map[string]int{"maps": 20}, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveMapOverlayCeiling(tc.requested, tc.limits); got != tc.want {
				t.Fatalf("effective map ceiling = %d, want %d", got, tc.want)
			}
		})
	}
}

package detail

import "testing"

func TestClassifySoundEffectUsesSpecificSemantics(t *testing.T) {
	tests := []struct {
		name, family, wantFamily, wantSubtype string
	}{
		{"sfx_impact_watery_sub_drop_01.wav", "impact", "impact", "watery_sub_drop"},
		{"sfx_impact_big_cinematic_sub_01.mp3", "impact", "impact", "cinematic_boom"},
		{"sfx_transition_zoom_sweep_01.wav", "transition", "transition", "camera_sweep"},
		{"woosh-building-109596.mp3", "", "whoosh", "whoosh"},
		{"sfx_glitch_data_transfer_01.wav", "glitch", "glitch", "digital_glitch"},
	}
	for _, tt := range tests {
		got := ClassifySoundEffect(tt.name, tt.family, nil)
		if got.Family != tt.wantFamily || got.Subtype != tt.wantSubtype {
			t.Fatalf("ClassifySoundEffect(%q, %q) = %s/%s, want %s/%s", tt.name, tt.family, got.Family, got.Subtype, tt.wantFamily, tt.wantSubtype)
		}
	}
}

func TestClassifySoundEffectAddsSelectionTags(t *testing.T) {
	got := ClassifySoundEffect("sfx_whoosh_arrow_fast_01.wav", "", nil)
	for _, want := range []string{"whoosh", "fast_swipe", "action", "high", "rank_change"} {
		found := false
		for _, tag := range append(got.Tags, got.BestFor...) {
			if tag == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("taxonomy missing selection label %q: tags=%v best_for=%v", want, got.Tags, got.BestFor)
		}
	}
}

package script

import "testing"

func TestFixedSectionNormalizesProtectedPlaybackAndDisplayText(t *testing.T) {
	section := FixedSection{
		ClipIDs:     []string{" intro-1 ", "intro-2"},
		DisplayText: "Opening card",
	}
	if got := section.EffectiveDisplayText(); got != "Opening card" {
		t.Fatalf("display text = %q, want %q", got, "Opening card")
	}
	playback := section.NormalizedPlayback()
	if playback.AudioMode != FixedPlaybackOriginalClip || !playback.Valid() {
		t.Fatalf("normalized playback = %+v, want valid original_clip policy", playback)
	}
	if got := section.NormalizedClipIDs(); len(got) != 2 || got[0] != "intro-1" || got[1] != "intro-2" {
		t.Fatalf("clip ids = %#v, want trimmed ids", got)
	}
}

func TestFixedPlaybackPolicyRejectsInvalidSourceWindows(t *testing.T) {
	cases := []FixedPlaybackPolicy{
		{AudioMode: FixedPlaybackAudioMode("voiceover")},
		{SourceInMS: 1000},
		{SourceOutMS: 1000},
		{SourceInMS: 2000, SourceOutMS: 1000},
	}
	for _, policy := range cases {
		if policy.Valid() {
			t.Errorf("policy %+v unexpectedly valid", policy)
		}
	}
}

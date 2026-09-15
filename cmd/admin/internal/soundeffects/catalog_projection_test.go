package soundeffects

import "testing"

func TestCanonicalProvidedSoundEffectsPrefersSharedEditorialCatalog(t *testing.T) {
	legacy := []providedSoundEffect{
		{driveID: "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq", filename: "wrong.mp3", name: "wrong"},
		{driveID: "legacy-only", filename: "legacy.mp3", name: "legacy"},
	}
	got := canonicalProvidedSoundEffects(legacy)
	if len(got) != 16 {
		t.Fatalf("projected catalog length = %d, want 16", len(got))
	}
	if got[2].driveID != "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq" || got[2].filename != "bgm3.mp3" || got[2].name != "HipHopSlowed" {
		t.Fatalf("shared BGM entry was not authoritative: %+v", got[2])
	}
	if got[len(got)-1].driveID != "legacy-only" {
		t.Fatalf("legacy projection entry was not preserved: %+v", got[len(got)-1])
	}
}

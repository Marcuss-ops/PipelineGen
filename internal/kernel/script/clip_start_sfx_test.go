package script

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClipStartSFXValidationAcceptsDeclaredClip(t *testing.T) {
	item := GenerationItemV2{
		Source: SourceSpec{ClipIDs: []string{"clip-a"}},
		Audio: AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{{
			ClipID: "clip-a", AssetID: "whoosh1",
		}}},
	}
	if details := validateAudioIntentBlock(item, "item"); len(details) != 0 {
		t.Fatalf("valid clip-start cue rejected: %v", details)
	}
}

func TestClipStartSFXValidationAcceptsIntroClip(t *testing.T) {
	item := GenerationItemV2{
		Intro: &FixedSection{ClipIDs: []string{"intro-a"}},
		Audio: AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{{
			ClipID: "intro-a", AssetID: "impact",
		}}},
	}
	if details := validateAudioIntentBlock(item, "item"); len(details) != 0 {
		t.Fatalf("intro-only clip-start cue rejected: %v", details)
	}
}

func TestClipStartSFXValidationFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		item GenerationItemV2
		want string
	}{
		{
			name: "unknown clip",
			item: GenerationItemV2{
				Source: SourceSpec{ClipIDs: []string{"clip-a"}},
				Audio: AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{{
					ClipID: "clip-b", AssetID: "whoosh1",
				}}},
			},
			want: "not declared",
		},
		{
			name: "duplicate clip",
			item: GenerationItemV2{
				Source: SourceSpec{ClipIDs: []string{"clip-a"}},
				Audio: AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{
					{ClipID: "clip-a", AssetID: "whoosh1"},
					{ClipID: "clip-a", AssetID: "whoosh2"},
				}},
			},
			want: "duplicate clip_id",
		},
		{
			name: "missing asset",
			item: GenerationItemV2{
				Source: SourceSpec{ClipIDs: []string{"clip-a"}},
				Audio: AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{{
					ClipID: "clip-a",
				}}},
			},
			want: "asset_id is required",
		},
		{
			name: "gain out of range",
			item: GenerationItemV2{
				Source: SourceSpec{ClipIDs: []string{"clip-a"}},
				Audio: AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{{
					ClipID: "clip-a", AssetID: "whoosh1", GainDB: ptrFloat64(25),
				}}},
			},
			want: "gain_db",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			details := validateAudioIntentBlock(tc.item, "item")
			if len(details) == 0 || !strings.Contains(strings.Join(details, "\n"), tc.want) {
				t.Fatalf("details = %v, want %q", details, tc.want)
			}
		})
	}
}

func TestClipStartSFXJSONRoundTripPreservesDefaults(t *testing.T) {
	cue := ClipStartSoundEffectIntent{ClipID: "clip-a", AssetID: "whoosh1"}
	if cue.EffectiveGainDB() != DefaultClipStartSFXGainDB || cue.EffectiveDurationMS() != DefaultClipStartSFXDurationMS {
		t.Fatalf("defaults = %v dB / %d ms", cue.EffectiveGainDB(), cue.EffectiveDurationMS())
	}
	raw, err := json.Marshal(AudioOutputConfig{ClipStartSoundEffects: []ClipStartSoundEffectIntent{cue}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded AudioOutputConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ClipStartSoundEffects) != 1 {
		t.Fatalf("clip_start_sound_effects = %+v", decoded.ClipStartSoundEffects)
	}
	got := decoded.ClipStartSoundEffects[0]
	if got.ClipID != "clip-a" || got.AssetID != "whoosh1" || got.EffectiveGainDB() != -6 || got.EffectiveDurationMS() != 250 {
		t.Fatalf("round-trip cue = %+v", got)
	}
}

func ptrFloat64(value float64) *float64 { return &value }

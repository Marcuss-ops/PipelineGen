// Package script — audio_fingerprint_test.go: regression-guard for the
// audio intent block's participation in the generation identity
// (idempotency / cache keys).
package script

import "testing"

func audioFingerprintItem() GenerationItemV2 {
	return GenerationItemV2{
		Title:    "audio-fp",
		Language: "en",
		Source:   SourceSpec{Type: SourceText, Topic: "topic", SourceText: "source text"},
		Audio: AudioOutputConfig{
			MixPolicy: "voiceover_with_ducked_clip",
			BackgroundMusic: []BackgroundMusicIntent{{
				AssetID: "bgm_01", StartMS: 0, Loop: true, GainDB: -24,
				End: &AudioTimelineEnd{VideoEnd: true},
			}},
			SoundEffects: []SoundEffectIntent{{AssetID: "whoosh", AtMS: 5000, GainDB: -8}},
		},
	}
}

func TestAudioFingerprint_Deterministic(t *testing.T) {
	item := audioFingerprintItem()
	first := BuildFingerprint(FingerprintInputFromItem(item))
	for i := 0; i < 5; i++ {
		if got := BuildFingerprint(FingerprintInputFromItem(item)); got != first {
			t.Fatalf("fingerprint not deterministic: %q vs %q", got, first)
		}
	}
}

// TestAudioFingerprint_AudioMutationsChangeIdentity certifies that every
// editorial audio fact (mix_policy, BGM asset, BGM window, SFX placement)
// invalidates the item identity — the idempotency/cache key must never
// survive an audio change.
func TestAudioFingerprint_AudioMutationsChangeIdentity(t *testing.T) {
	base := BuildFingerprint(FingerprintInputFromItem(audioFingerprintItem()))
	mutations := []struct {
		name string
		item GenerationItemV2
	}{
		{name: "mix_policy_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.MixPolicy = "VOICEOVER_ONLY"
			return it
		}()},
		{name: "bgm_asset_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.BackgroundMusic[0].AssetID = "bgm_02"
			return it
		}()},
		{name: "bgm_window_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.BackgroundMusic[0].End = &AudioTimelineEnd{Ms: 60_000}
			return it
		}()},
		{name: "bgm_loop_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.BackgroundMusic[0].Loop = false
			return it
		}()},
		{name: "bgm_gain_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.BackgroundMusic[0].GainDB = -18
			return it
		}()},
		{name: "sfx_asset_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.SoundEffects[0].AssetID = "impact"
			return it
		}()},
		{name: "sfx_placement_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.SoundEffects[0].AtMS = 12_500
			return it
		}()},
		{name: "sfx_removed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.SoundEffects = nil
			return it
		}()},
		{name: "audio_mode_changed", item: func() GenerationItemV2 {
			it := audioFingerprintItem()
			it.Audio.Mode = "COMBINED_TIMELINE"
			return it
		}()},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildFingerprint(FingerprintInputFromItem(tt.item))
			if got == base {
				t.Fatalf("changing %s must change the fingerprint (both %q)", tt.name, got)
			}
		})
	}
}

// TestAudioFingerprint_NoAudioBlockPreservesLegacyIdentity certifies that
// an item without an audio block keeps its pre-audio identity byte for
// byte (nil audio intent contributes nothing to the hash).
func TestAudioFingerprint_NoAudioBlockPreservesLegacyIdentity(t *testing.T) {
	legacy := GenerationItemV2{
		Title:    "plain",
		Language: "en",
		Source:   SourceSpec{Type: SourceText, Topic: "topic", SourceText: "source text"},
	}
	withZeroAudio := legacy
	withZeroAudio.Audio = AudioOutputConfig{}
	if BuildFingerprint(FingerprintInputFromItem(legacy)) != BuildFingerprint(FingerprintInputFromItem(withZeroAudio)) {
		t.Fatal("zero audio config must not change the legacy identity")
	}
}

// TestAudioFingerprint_OrderPreserved certifies that BGM/SFX order is
// part of the identity (a reorder is a different intent, not a no-op).
func TestAudioFingerprint_OrderPreserved(t *testing.T) {
	base := audioFingerprintItem()
	swapped := audioFingerprintItem()
	swapped.Audio.BackgroundMusic = append([]BackgroundMusicIntent{
		{AssetID: "bgm_00"},
	}, swapped.Audio.BackgroundMusic...)
	swapped.Audio.SoundEffects = append([]SoundEffectIntent{
		{AssetID: "swoosh2"},
	}, swapped.Audio.SoundEffects...)
	if BuildFingerprint(FingerprintInputFromItem(base)) == BuildFingerprint(FingerprintInputFromItem(swapped)) {
		t.Fatal("changing audio slice order must change the fingerprint")
	}
}

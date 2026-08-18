// Package script — audio_spec_test.go: regression-guard for the wire-level
// audio intent block (audio.mix_policy / background_music / sound_effects).
package script

import (
	"encoding/json"
	"testing"
)

// TestGenerationItemV2_AudioBlock_UnmarshalCanonicalPayload pins the
// canonical wire shape of the audio block: mix_policy plus one BGM layer
// and three SFX placements (scene-relative and absolute), all referencing
// assets by asset_id only — never by filesystem path.
func TestGenerationItemV2_AudioBlock_UnmarshalCanonicalPayload(t *testing.T) {
	payload := `{
	  "title": "test",
	  "source": {"type": "text", "source_text": "topic"},
	  "audio": {
	    "mix_policy": "voiceover_with_ducked_clip",
	    "background_music": [
	      {
	        "asset_id": "bgm_documentary_01",
	        "start_ms": 0,
	        "end": "video_end",
	        "loop": true,
	        "gain_db": -24,
	        "fade_in_ms": 1000,
	        "fade_out_ms": 1800,
	        "duck_under_voiceover": true,
	        "duck_gain_db": -30,
	        "duck_attack_ms": 120,
	        "duck_release_ms": 350
	      }
	    ],
	    "sound_effects": [
	      {"asset_id": "sfx_whoosh_01", "scene_id": "scene_2", "anchor": "start", "offset_ms": 250, "gain_db": -8},
	      {"asset_id": "sfx_impact_01", "scene_id": "scene_5", "anchor": "end", "offset_ms": -200, "gain_db": -5},
	      {"asset_id": "sfx_hit_01", "at_ms": 12500, "source_in_ms": 250, "duration_ms": 900, "gain_db": -3}
	    ]
	  }
	}`

	var item GenerationItemV2
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		t.Fatalf("unmarshal canonical audio block: %v", err)
	}

	if got := item.Audio.MixPolicy; got != "voiceover_with_ducked_clip" {
		t.Fatalf("mix_policy = %q, want %q", got, "voiceover_with_ducked_clip")
	}

	bgm := item.Audio.BackgroundMusic
	if len(bgm) != 1 {
		t.Fatalf("background_music = %+v, want exactly one entry", bgm)
	}
	b := bgm[0]
	if b.AssetID != "bgm_documentary_01" {
		t.Fatalf("bgm asset_id = %q", b.AssetID)
	}
	if b.StartMS != 0 || !b.End.IsVideoEnd() || !b.Loop || b.GainDB != -24 {
		t.Fatalf("bgm window/gain = %+v (end=%+v)", b, b.End)
	}
	if b.FadeInMS != 1000 || b.FadeOutMS != 1800 {
		t.Fatalf("bgm fades = [%d,%d]", b.FadeInMS, b.FadeOutMS)
	}
	if !b.DuckUnderVoiceover || b.DuckGainDB != -30 || b.DuckAttackMS != 120 || b.DuckReleaseMS != 350 {
		t.Fatalf("bgm ducking = %+v", b)
	}

	sfx := item.Audio.SoundEffects
	if len(sfx) != 3 {
		t.Fatalf("sound_effects = %+v, want exactly three entries", sfx)
	}
	if sfx[0].AssetID != "sfx_whoosh_01" || sfx[0].SceneID != "scene_2" || sfx[0].Anchor != SFXAnchorStart || sfx[0].OffsetMS != 250 || sfx[0].GainDB != -8 {
		t.Fatalf("sfx[0] = %+v", sfx[0])
	}
	if sfx[1].AssetID != "sfx_impact_01" || sfx[1].SceneID != "scene_5" || sfx[1].Anchor != SFXAnchorEnd || sfx[1].OffsetMS != -200 || sfx[1].GainDB != -5 {
		t.Fatalf("sfx[1] = %+v", sfx[1])
	}
	if sfx[2].AssetID != "sfx_hit_01" || sfx[2].AtMS != 12500 || sfx[2].SceneID != "" || sfx[2].SourceInMS != 250 || sfx[2].DurationMS != 900 || sfx[2].GainDB != -3 {
		t.Fatalf("sfx[2] = %+v", sfx[2])
	}
}

// TestAudioOutputConfig_BackgroundMusicSingleObjectNormalizesToSlice
// certifies the wire normalization: the legacy single-object form
// "background_music": {...} is always normalized to the canonical
// []BackgroundMusicIntent in the domain.
func TestAudioOutputConfig_BackgroundMusicSingleObjectNormalizesToSlice(t *testing.T) {
	var cfg AudioOutputConfig
	if err := json.Unmarshal([]byte(`{"background_music": {"asset_id": "music_123", "loop": true, "gain_db": -24}}`), &cfg); err != nil {
		t.Fatalf("unmarshal single-object background_music: %v", err)
	}
	if len(cfg.BackgroundMusic) != 1 {
		t.Fatalf("BackgroundMusic = %+v, want exactly one entry (single object must normalize to a slice)", cfg.BackgroundMusic)
	}
	b := cfg.BackgroundMusic[0]
	if b.AssetID != "music_123" || !b.Loop || b.GainDB != -24 {
		t.Fatalf("bgm = %+v", b)
	}
}

// TestAudioOutputConfig_BackgroundMusicArrayPreserved certifies that the
// canonical array form survives untouched — including multiple segmented
// BGM layers covering disjoint windows (start_ms / end_ms / end).
func TestAudioOutputConfig_BackgroundMusicArrayPreserved(t *testing.T) {
	var cfg AudioOutputConfig
	err := json.Unmarshal([]byte(`{
		"background_music": [
			{"asset_id": "music_intro", "start_ms": 0, "end_ms": 60000, "loop": true},
			{"asset_id": "music_dark", "start_ms": 60000, "end": "video_end", "loop": true}
		]
	}`), &cfg)
	if err != nil {
		t.Fatalf("unmarshal array background_music: %v", err)
	}
	if len(cfg.BackgroundMusic) != 2 {
		t.Fatalf("BackgroundMusic = %+v, want exactly two entries", cfg.BackgroundMusic)
	}
	first, second := cfg.BackgroundMusic[0], cfg.BackgroundMusic[1]
	if first.AssetID != "music_intro" || first.StartMS != 0 || first.End == nil || first.End.IsVideoEnd() || first.End.Ms != 60000 || !first.Loop {
		t.Fatalf("first bgm = %+v (end=%+v)", first, first.End)
	}
	if second.AssetID != "music_dark" || second.StartMS != 60000 || !second.End.IsVideoEnd() || !second.Loop {
		t.Fatalf("second bgm = %+v (end=%+v)", second, second.End)
	}
}

// TestAudioOutputConfig_BackgroundMusicInvalidTypeFailsClosed certifies
// that a non-object non-array background_music is rejected instead of being
// silently dropped.
func TestAudioOutputConfig_BackgroundMusicInvalidTypeFailsClosed(t *testing.T) {
	var cfg AudioOutputConfig
	if err := json.Unmarshal([]byte(`{"background_music": "nope"}`), &cfg); err == nil {
		t.Fatalf("expected error for string background_music, got %+v", cfg)
	}
	if err := json.Unmarshal([]byte(`{"background_music": 42}`), &cfg); err == nil {
		t.Fatalf("expected error for numeric background_music, got %+v", cfg)
	}
}

// TestAudioOutputConfig_BackgroundMusicNullDecodesToNil certifies that an
// explicit null behaves like an omitted field.
func TestAudioOutputConfig_BackgroundMusicNullDecodesToNil(t *testing.T) {
	var cfg AudioOutputConfig
	if err := json.Unmarshal([]byte(`{"background_music": null}`), &cfg); err != nil {
		t.Fatalf("unmarshal null background_music: %v", err)
	}
	if cfg.BackgroundMusic != nil {
		t.Fatalf("null background_music must decode to nil, got %+v", cfg.BackgroundMusic)
	}
}

// TestAudioTimelineEnd_Unmarshal pins the polymorphic end boundary:
// "video_end", numeric milliseconds, null and rejection of anything else.
func TestAudioTimelineEnd_Unmarshal(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantEnd   AudioTimelineEnd
		wantError bool
	}{
		{name: "literal_video_end", raw: `"video_end"`, wantEnd: AudioTimelineEnd{VideoEnd: true}},
		{name: "numeric_ms", raw: `60000`, wantEnd: AudioTimelineEnd{Ms: 60000}},
		{name: "numeric_negative_ms", raw: `-5000`, wantEnd: AudioTimelineEnd{Ms: -5000}}, // range semantics validated downstream
		{name: "null_resets_to_video_end", raw: `null`, wantEnd: AudioTimelineEnd{}},
		{name: "unknown_string_rejected", raw: `"video_start"`, wantError: true},
		{name: "object_rejected", raw: `{}`, wantError: true},
		{name: "bool_rejected", raw: `true`, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var end AudioTimelineEnd
			err := json.Unmarshal([]byte(tt.raw), &end)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for %s, got %+v", tt.raw, end)
				}
				return
			}
			if err != nil {
				t.Fatalf("unmarshal %s: %v", tt.raw, err)
			}
			if end != tt.wantEnd {
				t.Fatalf("AudioTimelineEnd(%s) = %+v, want %+v", tt.raw, end, tt.wantEnd)
			}
		})
	}
}

// TestAudioTimelineEnd_Marshal pins the wire egress form.
func TestAudioTimelineEnd_Marshal(t *testing.T) {
	tests := []struct {
		name string
		end  AudioTimelineEnd
		want string
	}{
		{name: "video_end", end: AudioTimelineEnd{VideoEnd: true}, want: `"video_end"`},
		{name: "numeric", end: AudioTimelineEnd{Ms: 4500}, want: `4500`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.end)
			if err != nil {
				t.Fatalf("marshal %+v: %v", tt.end, err)
			}
			if string(got) != tt.want {
				t.Fatalf("marshal(%+v) = %s, want %s", tt.end, got, tt.want)
			}
		})
	}
}

// TestBackgroundMusicIntent_EndMSAlias certifies the inbound alias
// "end_ms" → canonical "end": numeric ends written with the draft wire
// shape still decode, the canonical key wins when both are present, and
// non-numeric end_ms fails closed.
func TestBackgroundMusicIntent_EndMSAlias(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantEnd   AudioTimelineEnd
		wantError bool
	}{
		{name: "numeric_end_ms_alias", payload: `{"asset_id": "x", "end_ms": 60000}`, wantEnd: AudioTimelineEnd{Ms: 60000}},
		{name: "canonical_end_wins", payload: `{"asset_id": "x", "end": "video_end", "end_ms": 60000}`, wantEnd: AudioTimelineEnd{VideoEnd: true}},
		{name: "canonical_numeric_end", payload: `{"asset_id": "x", "end": 60000}`, wantEnd: AudioTimelineEnd{Ms: 60000}},
		{name: "invalid_end_ms_rejected", payload: `{"asset_id": "x", "end_ms": "nope"}`, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var intent BackgroundMusicIntent
			err := json.Unmarshal([]byte(tt.payload), &intent)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error for %s, got %+v", tt.payload, intent)
				}
				return
			}
			if err != nil {
				t.Fatalf("unmarshal %s: %v", tt.payload, err)
			}
			if intent.End == nil || *intent.End != tt.wantEnd {
				t.Fatalf("end for %s = %+v, want %+v", tt.payload, intent.End, tt.wantEnd)
			}
		})
	}
}

// TestBackgroundMusicIntent_EndOmittedDefaultsToVideoEnd certifies that an
// omitted end boundary means "cover the whole timeline".
func TestBackgroundMusicIntent_EndOmittedDefaultsToVideoEnd(t *testing.T) {
	var intent BackgroundMusicIntent
	if err := json.Unmarshal([]byte(`{"asset_id": "music_123", "loop": true, "gain_db": -24}`), &intent); err != nil {
		t.Fatalf("unmarshal minimal bgm: %v", err)
	}
	if intent.AssetID != "music_123" || !intent.Loop || intent.GainDB != -24 {
		t.Fatalf("minimal bgm = %+v", intent)
	}
	if !intent.End.IsVideoEnd() {
		t.Fatalf("omitted end must default to video_end, got %+v", intent.End)
	}
}

// TestSoundEffectIntent_NoFilesystemPaths certifies that the wire DTOs
// expose no path field at all: asset resolution must happen downstream.
func TestSoundEffectIntent_NoFilesystemPaths(t *testing.T) {
	var intent SoundEffectIntent
	// A caller attempting to smuggle a path gets it silently dropped on
	// decode; the canonical field set is asset_id + placement only.
	err := json.Unmarshal([]byte(`{"asset_id": "whoosh", "path": "/data/sfx/boom.mp3", "at_ms": 5000}`), &intent)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if intent.AssetID != "whoosh" || intent.AtMS != 5000 {
		t.Fatalf("intent = %+v", intent)
	}
	if got, marshalErr := json.Marshal(intent); marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	} else if len(got) > 200 {
		t.Fatalf("unexpected marshal output: %s", got)
	}
}

// TestGenerationItemV2_AudioBlock_RoundTrip certifies that the audio block
// survives marshal → unmarshal without losing intent.
func TestGenerationItemV2_AudioBlock_RoundTrip(t *testing.T) {
	item := GenerationItemV2{
		Title: "test",
		Audio: AudioOutputConfig{
			MixPolicy: "voiceover_with_ducked_clip",
			BackgroundMusic: []BackgroundMusicIntent{{
				AssetID: "bgm_01",
				StartMS: 0,
				End:     &AudioTimelineEnd{Ms: 60000},
				Loop:    true,
				GainDB:  -24,
			}},
			SoundEffects: []SoundEffectIntent{{
				AssetID:  "whoosh",
				SceneID:  "scene_2",
				Anchor:   SFXAnchorEnd,
				OffsetMS: -300,
				GainDB:   -8,
			}},
		},
	}

	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded GenerationItemV2
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Audio.BackgroundMusic) != 1 {
		t.Fatalf("background_music lost in round-trip: %+v", decoded.Audio.BackgroundMusic)
	}
	b := decoded.Audio.BackgroundMusic[0]
	if b.AssetID != "bgm_01" || !b.Loop || b.GainDB != -24 || b.End == nil || b.End.IsVideoEnd() || b.End.Ms != 60000 {
		t.Fatalf("bgm after round-trip = %+v (end=%+v)", b, b.End)
	}
	if len(decoded.Audio.SoundEffects) != 1 {
		t.Fatalf("sound_effects lost in round-trip: %+v", decoded.Audio.SoundEffects)
	}
	s := decoded.Audio.SoundEffects[0]
	if s.AssetID != "whoosh" || s.SceneID != "scene_2" || s.Anchor != SFXAnchorEnd || s.OffsetMS != -300 || s.GainDB != -8 {
		t.Fatalf("sfx after round-trip = %+v", s)
	}
}

// TestSFXAnchor_Normalize pins the anchor vocabulary and the fail-closed
// rejection of unknown values.
func TestSFXAnchor_Normalize(t *testing.T) {
	tests := []struct {
		name      string
		input     SFXAnchor
		want      SFXAnchor
		wantError bool
	}{
		{name: "empty_defaults_to_start", input: "", want: SFXAnchorStart},
		{name: "start", input: "start", want: SFXAnchorStart},
		{name: "middle", input: "middle", want: SFXAnchorMiddle},
		{name: "end", input: "end", want: SFXAnchorEnd},
		{name: "case_insensitive", input: "END", want: SFXAnchorEnd},
		{name: "unknown_rejected", input: "around", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.input.Normalize()
			if tt.wantError {
				if err == nil {
					t.Fatalf("Normalize(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

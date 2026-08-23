// Package script — audio_intent_validation_test.go: regression-guard for
// the structural validation of the audio intent block.
package script

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// audioValidationEnvelope builds an envelope whose item carries the given
// raw audio block, decoded through the same JSON path the HTTP layer uses
// (AudioOutputConfig.UnmarshalJSON wire normalization included). An empty
// block means the audio key is omitted entirely.
func audioValidationEnvelope(audioJSON string) *GenerationEnvelopeV2 {
	raw := `{"version":2,"items":[{"title":"audio-validation","source":{"type":"text","topic":"topic"}`
	if audioJSON != "" {
		raw += `,"audio":` + audioJSON
	}
	raw += `}]}`
	var env GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		panic(err)
	}
	return &env
}

// audioValidationEnvelopeWithDuration builds the same envelope but declares
// a target timeline duration (script_params.duration, seconds) so the
// timeline-coherence checks of the BGM window can be exercised.
func audioValidationEnvelopeWithDuration(audioJSON string, durationSec int) *GenerationEnvelopeV2 {
	raw := `{"version":2,"items":[{"title":"audio-validation","source":{"type":"text","topic":"topic"},"script_params":{"duration":` + strconv.Itoa(durationSec) + `}`
	if audioJSON != "" {
		raw += `,"audio":` + audioJSON
	}
	raw += `}]}`
	var env GenerationEnvelopeV2
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		panic(err)
	}
	return &env
}

func TestValidateAudioIntentBlock_CanonicalPayloadIsValid(t *testing.T) {
	env := audioValidationEnvelope(`{
		"mix_policy": "voiceover_with_ducked_clip",
		"background_music": [{
			"asset_id": "bgm_01", "start_ms": 0, "end": "video_end", "loop": true,
			"gain_db": -24, "fade_in_ms": 1000, "fade_out_ms": 1800,
			"duck_under_voiceover": true, "duck_gain_db": -30, "duck_attack_ms": 120, "duck_release_ms": 350
		}],
		"sound_effects": [
			{"asset_id": "whoosh", "scene_id": "scene_2", "anchor": "end", "offset_ms": -300, "gain_db": -8},
			{"asset_id": "hit", "at_ms": 12500, "source_in_ms": 250, "duration_ms": 900, "gain_db": -3}
		]
	}`)
	if err := env.Validate(); err != nil {
		t.Fatalf("canonical audio block must validate: %v", err)
	}
}

func TestValidateAudioIntentBlock_NoAudioBlockIsValid(t *testing.T) {
	env := audioValidationEnvelope("")
	if err := env.Validate(); err != nil {
		t.Fatalf("envelope without audio block must validate: %v", err)
	}
}

func TestValidateAudioIntentBlock_FailClosed(t *testing.T) {
	tests := []struct {
		name    string
		audio   string
		wantSub string
	}{
		{name: "unsupported_mix_policy", audio: `{"mix_policy": "duck_everything"}`, wantSub: "unsupported audio.mix_policy"},
		{name: "bgm_missing_asset_id", audio: `{"background_music": [{"start_ms": 0}]}`, wantSub: "background_music[0]: asset_id is required"},
		{name: "bgm_negative_start", audio: `{"background_music": [{"asset_id": "m", "start_ms": -1}]}`, wantSub: "start_ms must be >= 0"},
		{name: "bgm_end_before_start", audio: `{"background_music": [{"asset_id": "m", "start_ms": 5000, "end_ms": 3000}]}`, wantSub: "end must be after start_ms"},
		{name: "bgm_negative_end", audio: `{"background_music": [{"asset_id": "m", "end_ms": -100}]}`, wantSub: "end must be >= 0"},
		{name: "bgm_gain_out_of_range", audio: `{"background_music": [{"asset_id": "m", "gain_db": -99}]}`, wantSub: "gain_db must be within"},
		{name: "bgm_negative_fade", audio: `{"background_music": [{"asset_id": "m", "fade_in_ms": -1}]}`, wantSub: "fade_in_ms and fade_out_ms must be >= 0"},
		{name: "bgm_positive_duck_gain", audio: `{"background_music": [{"asset_id": "m", "duck_under_voiceover": true, "duck_gain_db": 3}]}`, wantSub: "duck_gain_db must be within"},
		{name: "bgm_negative_duck_ramp", audio: `{"background_music": [{"asset_id": "m", "duck_under_voiceover": true, "duck_attack_ms": -1}]}`, wantSub: "duck_attack_ms and duck_release_ms must be >= 0"},
		{name: "sfx_missing_asset_id", audio: `{"sound_effects": [{"at_ms": 1000}]}`, wantSub: "sound_effects[0]: asset_id is required"},
		{name: "sfx_negative_at_ms", audio: `{"sound_effects": [{"asset_id": "s", "at_ms": -1}]}`, wantSub: "at_ms must be >= 0"},
		{name: "sfx_dual_placement", audio: `{"sound_effects": [{"asset_id": "s", "at_ms": 1000, "scene_id": "scene_1"}]}`, wantSub: "mutually exclusive"},
		{name: "sfx_anchor_without_scene", audio: `{"sound_effects": [{"asset_id": "s", "at_ms": 1000, "anchor": "end"}]}`, wantSub: "anchor requires scene_id"},
		{name: "sfx_offset_without_scene", audio: `{"sound_effects": [{"asset_id": "s", "at_ms": 1000, "offset_ms": 200}]}`, wantSub: "offset_ms requires scene_id"},
		{name: "sfx_invalid_anchor", audio: `{"sound_effects": [{"asset_id": "s", "scene_id": "scene_1", "anchor": "around"}]}`, wantSub: "invalid sfx anchor"},
		{name: "sfx_negative_trim", audio: `{"sound_effects": [{"asset_id": "s", "at_ms": 1000, "duration_ms": -1}]}`, wantSub: "source_in_ms and duration_ms must be >= 0"},
		{name: "sfx_gain_out_of_range", audio: `{"sound_effects": [{"asset_id": "s", "at_ms": 1000, "gain_db": 20}]}`, wantSub: "gain_db must be within"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := audioValidationEnvelope(tt.audio)
			err := env.Validate()
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error for %s = %v, want substring %q", tt.name, err, tt.wantSub)
			}
		})
	}
}

// TestValidateAudioIntentBlock_EndCoherentWithDeclaredTimeline certifies
// the payload-level BGM-window coherence check against the declared
// script_params.duration: a numeric end must not run past the timeline and
// the window start must be before the timeline end. video_end is always
// coherent (it means "whatever the final timeline is").
func TestValidateAudioIntentBlock_EndCoherentWithDeclaredTimeline(t *testing.T) {
	// An explicit numeric end equal to the declared timeline is coherent
	// (exclusive end == video end).
	ok := audioValidationEnvelopeWithDuration(`{"background_music": [{"asset_id": "m", "start_ms": 0, "end_ms": 60000}]}`, 60)
	if err := ok.Validate(); err != nil {
		t.Fatalf("end == declared timeline must validate: %v", err)
	}

	// video_end with any declared duration stays coherent.
	videoEnd := audioValidationEnvelopeWithDuration(`{"background_music": [{"asset_id": "m", "start_ms": 0, "end": "video_end"}]}`, 60)
	if err := videoEnd.Validate(); err != nil {
		t.Fatalf("video_end must validate against any declared timeline: %v", err)
	}

	fail := []struct {
		name    string
		audio   string
		wantSub string
	}{
		{name: "end_beyond_timeline", audio: `{"background_music": [{"asset_id": "m", "end_ms": 40000}]}`, wantSub: "end must not exceed the 30000ms declared timeline"},
		{name: "start_beyond_timeline", audio: `{"background_music": [{"asset_id": "m", "start_ms": 30000}]}`, wantSub: "start_ms must be before the 30000ms declared timeline"},
	}
	for _, tt := range fail {
		t.Run(tt.name, func(t *testing.T) {
			env := audioValidationEnvelopeWithDuration(tt.audio, 30)
			err := env.Validate()
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error for %s = %v, want substring %q", tt.name, err, tt.wantSub)
			}
		})
	}
}

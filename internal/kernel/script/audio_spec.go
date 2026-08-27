// Package script — audio_spec.go defines the wire-level audio intent
// block of GenerationItemV2.Audio (JSON key "audio"):
//
//	"audio": {
//	  "mix_policy": "voiceover_with_ducked_clip",
//	  "background_music": [ ... ],
//	  "sound_effects": [ ... ]
//	}
//
// These DTOs express the caller's editorial INTENT, not the compiled
// FFmpeg plan. They reference assets exclusively by asset_id — filesystem
// paths are never accepted at the wire boundary. Resolving asset_id to a
// physical path happens downstream (AudioAssetResolver), so the API,
// Drive and filesystem stay decoupled.
package script

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// UnmarshalJSON decodes AudioOutputConfig and normalizes the
// background_music wire shape: both the canonical array form and the
// single-object form are accepted, and the domain ALWAYS works with
// []BackgroundMusicIntent. Segmenting the BGM into multiple layers
// later therefore needs no schema change — the payload just switches
// from one object to an array of objects.
//
// The raw-map pre-pass runs before the alias decode so a malformed
// payload still fails closed with the standard json type error
// (e.g. background_music as a string).
func (a *AudioOutputConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if bgm, ok := raw["background_music"]; ok {
		trimmed := bytes.TrimSpace(bgm)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			raw["background_music"] = []byte("[" + string(trimmed) + "]")
		}
	}
	normalized, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	type Alias AudioOutputConfig
	var tmp Alias
	if err := json.Unmarshal(normalized, &tmp); err != nil {
		return err
	}
	*a = AudioOutputConfig(tmp)
	return nil
}

// UnmarshalJSON decodes a BackgroundMusicIntent and maps the numeric
// alias "end_ms" onto the canonical "end" boundary when the canonical
// key is absent. Egress always uses "end" (SSOT); "end_ms" is accepted
// inbound so segmented multi-music payloads written against the earlier
// draft wire shape keep working. When both keys are present the canonical
// "end" wins.
func (b *BackgroundMusicIntent) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, hasEnd := raw["end"]; !hasEnd {
		if endMS, ok := raw["end_ms"]; ok {
			raw["end"] = endMS
			delete(raw, "end_ms")
		}
	}
	normalized, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	type Alias BackgroundMusicIntent
	var tmp Alias
	if err := json.Unmarshal(normalized, &tmp); err != nil {
		return err
	}
	if isBuiltInBGM(tmp.AssetID) {
		if _, present := raw["loop"]; !present {
			tmp.Loop = true
		}
		if _, present := raw["gain_db"]; !present {
			tmp.GainDB = -30
		}
	}
	*b = BackgroundMusicIntent(tmp)
	return nil
}

func isBuiltInBGM(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.HasPrefix(id, "bgm") && len(id) == 4 && id[3] >= '1' && id[3] <= '6'
}

func isBuiltInWhoop(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.HasPrefix(id, "whop") || strings.HasPrefix(id, "whoop")
}

func isBuiltInWhoosh(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	return strings.HasPrefix(id, "whoosh") || id == "random_whoosh"
}

// BackgroundMusicIntent is the caller's intent for one background-music
// layer. A segment may declare several layers to cover different windows
// of the video. The zero value means "cover the whole timeline".
type BackgroundMusicIntent struct {
	// AssetID references the music asset in the asset store. Never a
	// filesystem path.
	AssetID string `json:"asset_id"`
	// StartMS is the timeline offset where the layer begins. Zero means
	// the start of the video.
	StartMS int64 `json:"start_ms,omitempty"`
	// End is the exclusive end boundary of the layer: the literal
	// "video_end" (the default when omitted) or an absolute millisecond
	// offset into the timeline.
	End *AudioTimelineEnd `json:"end,omitempty"`
	// Loop repeats the source from the beginning once it runs out, so the
	// layer covers its whole window exactly. False means the layer stops
	// when the source ends (silence BGM until the window end).
	Loop bool `json:"loop,omitempty"`
	// GainDB is the static layer gain. Zero means unity (0 dB).
	GainDB float64 `json:"gain_db,omitempty"`
	// FadeInMS and FadeOutMS ramp the layer from/to silence at the window
	// edges. Zero means no fade.
	FadeInMS  int64 `json:"fade_in_ms,omitempty"`
	FadeOutMS int64 `json:"fade_out_ms,omitempty"`
	// DuckUnderVoiceover lowers the layer while the voiceover speaks.
	// DuckGainDB is the ducked level, DuckAttackMS/DuckReleaseMS are the
	// ramp times into and out of the ducked state.
	DuckUnderVoiceover bool    `json:"duck_under_voiceover,omitempty"`
	DuckGainDB         float64 `json:"duck_gain_db,omitempty"`
	DuckAttackMS       int64   `json:"duck_attack_ms,omitempty"`
	DuckReleaseMS      int64   `json:"duck_release_ms,omitempty"`
}

// SoundEffectIntent is the caller's intent for one sound effect. The
// effect is placed either at an absolute timeline offset (AtMS) or
// relative to a scene (SceneID + Anchor + OffsetMS) — never both.
type SoundEffectIntent struct {
	// AssetID references the effect asset in the asset store. Never a
	// filesystem path.
	AssetID string `json:"asset_id"`
	// AtMS places the effect at an absolute timeline offset. Mutually
	// exclusive with SceneID.
	AtMS int64 `json:"at_ms,omitempty"`
	// SceneID places the effect relative to the named scene. When set,
	// Anchor selects the reference point and OffsetMS shifts it (signed).
	SceneID string `json:"scene_id,omitempty"`
	// Anchor is the scene reference point for SceneID placement:
	// "start" (default), "middle" or "end".
	Anchor SFXAnchor `json:"anchor,omitempty"`
	// OffsetMS shifts the effect relative to the anchor. Negative values
	// move it earlier (e.g. 300 ms before the scene end).
	OffsetMS int64 `json:"offset_ms,omitempty"`
	// SourceInMS and DurationMS trim the source inside the event. Zero
	// means the whole source plays from its start.
	SourceInMS int64 `json:"source_in_ms,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"`
	// GainDB is the effect gain. Zero means unity (0 dB).
	GainDB float64 `json:"gain_db,omitempty"`
}

// UnmarshalJSON applies safe defaults for the built-in one-shot catalog.
// Explicit caller values always win, including gain_db: 0 and loop:false
// when those keys are present on the wire.
func (s *SoundEffectIntent) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	type Alias SoundEffectIntent
	var tmp Alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	if _, present := raw["gain_db"]; !present && (isBuiltInWhoop(tmp.AssetID) || isBuiltInWhoosh(tmp.AssetID)) {
		tmp.GainDB = -30
	}
	*s = SoundEffectIntent(tmp)
	return nil
}

// SFXAnchor is the scene reference point used for scene-relative SFX
// placement.
type SFXAnchor string

const (
	SFXAnchorStart  SFXAnchor = "start"
	SFXAnchorMiddle SFXAnchor = "middle"
	SFXAnchorEnd    SFXAnchor = "end"
)

// Normalize returns the canonical anchor. An empty value maps to the
// default "start" (an offset without an anchor is relative to the scene
// start). Unknown values fail closed.
func (a SFXAnchor) Normalize() (SFXAnchor, error) {
	switch SFXAnchor(strings.ToLower(strings.TrimSpace(string(a)))) {
	case "", SFXAnchorStart:
		return SFXAnchorStart, nil
	case SFXAnchorMiddle:
		return SFXAnchorMiddle, nil
	case SFXAnchorEnd:
		return SFXAnchorEnd, nil
	default:
		return "", fmt.Errorf("script: invalid sfx anchor %q (canonical: start, middle, end)", string(a))
	}
}

// AudioTimelineEnd is the polymorphic end boundary of a BGM layer. Its
// wire form is either the literal "video_end" or a JSON number carrying
// an absolute millisecond offset. The zero value means "video_end".
type AudioTimelineEnd struct {
	// VideoEnd marks the boundary as the end of the video timeline.
	VideoEnd bool
	// Ms is the absolute millisecond offset. Meaningful only when
	// VideoEnd is false.
	Ms int64
}

// IsVideoEnd reports whether the boundary is the end of the video (nil
// receiver counts as the default "video_end").
func (e *AudioTimelineEnd) IsVideoEnd() bool {
	return e == nil || e.VideoEnd
}

// MarshalJSON emits the canonical wire form: "video_end" or the numeric
// millisecond offset.
func (e AudioTimelineEnd) MarshalJSON() ([]byte, error) {
	if e.VideoEnd {
		return []byte(`"video_end"`), nil
	}
	return json.Marshal(e.Ms)
}

// UnmarshalJSON accepts the literal string "video_end", a JSON number
// (millisecond offset), or null (which resets to the default
// "video_end"). Any other payload fails closed.
func (e *AudioTimelineEnd) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*e = AudioTimelineEnd{}
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if strings.TrimSpace(asString) != "video_end" {
			return &audioTimelineEndInvalidError{data: data}
		}
		*e = AudioTimelineEnd{VideoEnd: true}
		return nil
	}
	var asMS int64
	if err := json.Unmarshal(data, &asMS); err == nil {
		*e = AudioTimelineEnd{Ms: asMS}
		return nil
	}
	return &audioTimelineEndInvalidError{data: data}
}

// audioTimelineEndInvalidError signals a wire payload that is neither
// "video_end" nor a millisecond number (operator-facing API fails
// closed).
type audioTimelineEndInvalidError struct {
	data []byte
}

func (e *audioTimelineEndInvalidError) Error() string {
	return "script: audio end must be \"video_end\" or a millisecond number; got: " +
		string(bytes.TrimSpace(e.data))
}

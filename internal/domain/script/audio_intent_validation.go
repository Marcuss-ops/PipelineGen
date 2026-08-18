// Package script — audio_intent_validation.go performs the structural
// validation of the wire-level audio intent block (audio.mix_policy /
// background_music / sound_effects) during GenerationEnvelopeV2.Validate.
//
// These checks are payload-level (structural shape only): timeline-aware
// facts (windows within the canonical timeline, scene existence) are
// validated by the intent resolvers at compile time, where the timeline
// is known.
package script

import (
	"fmt"
	"strings"
)

// Editorial gain range accepted for audio intents. Zero means unity /
// "use the default" and is exempt from the range check. The ducked gain
// must never raise the layer (ducking lowers it), so its ceiling is 0 dB.
const (
	AudioGainMinDB = -60.0
	AudioGainMaxDB = 6.0
)

// validateAudioIntentBlock returns a list of structural violations found
// in the item's audio intent block. An empty list means the block is
// structurally valid. The canonical location is the top-level
// item.Audio config; the legacy nested output.audio shape is not part of
// the intent contract.
func validateAudioIntentBlock(item GenerationItemV2, ref string) []string {
	cfg := item.Audio
	var details []string

	if raw := strings.TrimSpace(string(cfg.MixPolicy)); raw != "" && cfg.MixPolicy.Normalize() == "" {
		details = append(details, ref+": unsupported audio.mix_policy "+raw)
	}

	for i, b := range cfg.BackgroundMusic {
		prefix := fmt.Sprintf("%s: audio.background_music[%d]", ref, i)
		if strings.TrimSpace(b.AssetID) == "" {
			details = append(details, prefix+": asset_id is required")
		}
		if b.StartMS < 0 {
			details = append(details, prefix+": start_ms must be >= 0")
		}
		if !b.End.IsVideoEnd() {
			if b.End.Ms < 0 {
				details = append(details, prefix+": end must be >= 0")
			} else if b.End.Ms <= b.StartMS {
				details = append(details, prefix+": end must be after start_ms")
			}
		}
		// Timeline coherence: when the item declares a target duration
		// (script_params.duration, seconds), the BGM window must stay inside
		// it. "video_end" is always coherent (it means "whatever the final
		// timeline is"); an explicit numeric end must not run past the
		// declared timeline. The authoritative check against the real
		// CanonicalTimeline still happens at compile time (the resolver
		// fails closed when the window exceeds the actual timeline) — this
		// is the early payload-level signal.
		if timelineMS := int64(item.ScriptParams.Duration) * 1000; timelineMS > 0 {
			if b.StartMS >= timelineMS {
				details = append(details, fmt.Sprintf("%s: start_ms must be before the %dms declared timeline", prefix, timelineMS))
			}
			if !b.End.IsVideoEnd() && b.End.Ms > timelineMS {
				details = append(details, fmt.Sprintf("%s: end must not exceed the %dms declared timeline", prefix, timelineMS))
			}
		}
		if b.GainDB != 0 && (b.GainDB < AudioGainMinDB || b.GainDB > AudioGainMaxDB) {
			details = append(details, fmt.Sprintf("%s: gain_db must be within [%.0f, %.0f] dB", prefix, AudioGainMinDB, AudioGainMaxDB))
		}
		if b.FadeInMS < 0 || b.FadeOutMS < 0 {
			details = append(details, prefix+": fade_in_ms and fade_out_ms must be >= 0")
		}
		if b.DuckUnderVoiceover {
			if b.DuckGainDB != 0 && (b.DuckGainDB < AudioGainMinDB || b.DuckGainDB > 0) {
				details = append(details, fmt.Sprintf("%s: duck_gain_db must be within [%.0f, 0] dB", prefix, AudioGainMinDB))
			}
			if b.DuckAttackMS < 0 || b.DuckReleaseMS < 0 {
				details = append(details, prefix+": duck_attack_ms and duck_release_ms must be >= 0")
			}
		}
	}

	for i, s := range cfg.SoundEffects {
		prefix := fmt.Sprintf("%s: audio.sound_effects[%d]", ref, i)
		if strings.TrimSpace(s.AssetID) == "" {
			details = append(details, prefix+": asset_id is required")
		}
		if s.AtMS < 0 {
			details = append(details, prefix+": at_ms must be >= 0")
		}
		if s.SceneID != "" && s.AtMS != 0 {
			details = append(details, prefix+": at_ms and scene_id are mutually exclusive")
		}
		if s.SceneID == "" && strings.TrimSpace(string(s.Anchor)) != "" {
			details = append(details, prefix+": anchor requires scene_id")
		}
		if s.SceneID == "" && s.OffsetMS != 0 {
			details = append(details, prefix+": offset_ms requires scene_id")
		}
		if _, err := s.Anchor.Normalize(); err != nil {
			details = append(details, prefix+": "+err.Error())
		}
		if s.SourceInMS < 0 || s.DurationMS < 0 {
			details = append(details, prefix+": source_in_ms and duration_ms must be >= 0")
		}
		if s.GainDB != 0 && (s.GainDB < AudioGainMinDB || s.GainDB > AudioGainMaxDB) {
			details = append(details, fmt.Sprintf("%s: gain_db must be within [%.0f, %.0f] dB", prefix, AudioGainMinDB, AudioGainMaxDB))
		}
	}

	return details
}

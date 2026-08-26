package audio

import "strings"

// AudioMixPolicy is the editorial decision for how the compiled master
// balances the generated voiceover against the original clip audio. It lives
// in the CompiledAudioPlan (never in the document renderer) so the mixer and
// the video renderer consume the same decision.
type AudioMixPolicy string

const (
	// MixVoiceoverOnly renders the voiceover alone; original clip audio is
	// not part of the final mix.
	MixVoiceoverOnly AudioMixPolicy = "VOICEOVER_ONLY"

	// MixVoiceoverWithDuckedClip keeps the voiceover at unity and places the
	// original clip audio underneath it at a fixed duck level, with dynamic
	// ducking that lowers the clip further while speech is active.
	MixVoiceoverWithDuckedClip AudioMixPolicy = "VOICEOVER_DUCKED_CLIP"
)

const (
	// BackgroundMusicGainDB and SoundEffectGainDB are canonical absolute
	// levels relative to the unity voiceover track. They are enforced by the
	// canonical audio compiler; payload gain_db values cannot override them.
	BackgroundMusicGainDB = -50.0
	SoundEffectGainDB     = -10.0

	// DuckClipBaseGainDB is the static clip-audio gain applied under the
	// VOICEOVER_DUCKED_CLIP policy when no explicit gain is set.
	DuckClipBaseGainDB = -18.0

	// DuckClipActiveGainDB is the deeper gain applied to the clip track while
	// the voiceover is actually speaking (the ducking automation target).
	DuckClipActiveGainDB = -24.0

	// DuckAttackUS and DuckReleaseUS are the automation ramp times applied at
	// the start and end of a ducking window.
	DuckAttackUS  = int64(100_000)
	DuckReleaseUS = int64(250_000)
)

// Normalize returns the canonical policy for the given value, or "" when the
// value is unknown or empty. An empty policy means "no mix policy applied",
// which preserves the legacy full-volume overlap behaviour.
//
// The wire spelling "VOICEOVER_WITH_DUCKED_CLIP" (documented in the HTTP
// payload as "voiceover_with_ducked_clip") is accepted as an alias of the
// canonical VOICEOVER_DUCKED_CLIP so the intent never depends on which side
// of the API boundary spelled it.
func (p AudioMixPolicy) Normalize() AudioMixPolicy {
	p = AudioMixPolicy(strings.ToUpper(strings.TrimSpace(string(p))))
	switch p {
	case MixVoiceoverOnly, MixVoiceoverWithDuckedClip:
		return p
	case "VOICEOVER_WITH_DUCKED_CLIP":
		return MixVoiceoverWithDuckedClip
	default:
		return ""
	}
}

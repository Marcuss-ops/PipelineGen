// Package scriptgeneration — audio_automation_compiler.go compiles the
// editorial audio automation (BGM fades + BGM ducking under voiceover)
// into canonical AudioAutomation entries on the compiled plan.
//
// Fades: a BGM layer with fades is expressed as ONE automation window
// spanning the whole layer window on the "bgm" track:
//
//	window start                window end
//	    │  attack (fade_in)         │ release (fade_out)
//	BGM -∞ ─────── ramp ───────► -24 dB ────── ramp ──────► -∞
//
// The renderer holds the layer gain between the ramps and fades from/to
// silence at the edges, so the master never cuts the music brutally at
// the window end (which, for end=video_end, is the video end).
//
// Ducking: each voiceover speech window that overlaps a ducking-enabled
// BGM layer produces one automation entry lowering the "bgm" track to the
// ducked gain while the voiceover actually speaks — the same
// AudioAutomation contract the clip ducking uses, so the mixer and
// renderer consume exactly one automation mechanism.
package scriptgeneration

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// AudioAutomationCompiler compiles resolved BGM layers into canonical
// automation entries. It is stateless and pure.
type AudioAutomationCompiler struct{}

// Plan defaults for BGM ducking under voiceover, applied when
// duck_under_voiceover is set without explicit values (mirror of the
// DuckClip* ducking constants in capabilities/audio/mix_policy.go).
const (
	DefaultBGMDuckGainDB    = -30.0
	DefaultBGMDuckAttackUS  = int64(120_000)
	DefaultBGMDuckReleaseUS = int64(350_000)
)

// NewAudioAutomationCompiler builds the canonical compiler. No
// dependencies.
func NewAudioAutomationCompiler() *AudioAutomationCompiler {
	return &AudioAutomationCompiler{}
}

// CompileBGMFades returns one AudioAutomation entry per BGM layer that
// carries at least one fade. Each entry targets the canonical "bgm" track
// (the same TrackID CompileWithLayers assigns to the BGM track) over the
// whole layer window: AttackUS = fade-in (ramp from silence to the layer
// gain), ReleaseUS = fade-out (ramp back to silence at the window end).
//
// Fades longer than the window are clamped to it (a 1.2s fade-in on a 1s
// window fades over the whole second). A layer without fades produces no
// entry. Negative fades and malformed layers fail closed.
func (r *AudioAutomationCompiler) CompileBGMFades(bgm []audio.ResolvedBGM) ([]audio.AudioAutomation, error) {
	var out []audio.AudioAutomation
	for i, layer := range bgm {
		if strings.TrimSpace(layer.AssetID) == "" {
			return nil, fmt.Errorf("scriptgeneration: bgm layer %d requires an asset_id", i)
		}
		if layer.TimelineStartUS < 0 || layer.DurationUS <= 0 {
			return nil, fmt.Errorf("scriptgeneration: bgm layer %d (%s) has an invalid window (start=%dus duration=%dus)", i, layer.AssetID, layer.TimelineStartUS, layer.DurationUS)
		}
		if layer.FadeInUS < 0 || layer.FadeOutUS < 0 {
			return nil, fmt.Errorf("scriptgeneration: bgm layer %d (%s) fades must be >= 0 (got in=%dus out=%dus)", i, layer.AssetID, layer.FadeInUS, layer.FadeOutUS)
		}
		if layer.FadeInUS == 0 && layer.FadeOutUS == 0 {
			continue
		}
		fadeIn := min(layer.FadeInUS, layer.DurationUS)
		fadeOut := min(layer.FadeOutUS, layer.DurationUS)
		if fadeIn == 0 && fadeOut == 0 {
			continue // window too small for either fade
		}
		out = append(out, audio.AudioAutomation{
			TargetTrackID: "bgm",
			StartUS:       layer.TimelineStartUS,
			EndUS:         layer.TimelineStartUS + layer.DurationUS,
			GainDB:        layer.GainDB,
			AttackUS:      fadeIn,
			ReleaseUS:     fadeOut,
		})
	}
	return out, nil
}

// CompileBGMDucking returns one AudioAutomation entry per voiceover
// speech window that overlaps a ducking-enabled BGM layer. Each entry
// targets the "bgm" track, is triggered by "voiceover", and lowers the
// layer to the ducked gain while the speech is actually present (the
// speech window ends at min(TimelineDurationUS, SourceDurationUS), like
// the clip ducking), with the intent's attack/release ramps.
//
// Zero DuckGainDB / DuckAttackUS / DuckReleaseUS use the plan defaults
// (-30 dB / 120 ms / 350 ms); negative values fail closed. Layers without
// duck_under_voiceover produce no entries.
func (r *AudioAutomationCompiler) CompileBGMDucking(timeline audio.CanonicalTimeline, bgm []audio.ResolvedBGM) ([]audio.AudioAutomation, error) {
	if err := timeline.Validate(); err != nil {
		return nil, fmt.Errorf("compile bgm ducking: %w", err)
	}
	speech := voiceoverSpeechWindows(timeline)
	var out []audio.AudioAutomation
	for i, layer := range bgm {
		if strings.TrimSpace(layer.AssetID) == "" {
			return nil, fmt.Errorf("compile bgm ducking: layer %d requires an asset_id", i)
		}
		if layer.TimelineStartUS < 0 || layer.DurationUS <= 0 {
			return nil, fmt.Errorf("compile bgm ducking: layer %d (%s) has an invalid window (start=%dus duration=%dus)", i, layer.AssetID, layer.TimelineStartUS, layer.DurationUS)
		}
		if !layer.DuckUnderVoiceover {
			continue
		}
		if layer.DuckAttackUS < 0 || layer.DuckReleaseUS < 0 {
			return nil, fmt.Errorf("compile bgm ducking: layer %d (%s) duck ramps must be >= 0 (got attack=%dus release=%dus)", i, layer.AssetID, layer.DuckAttackUS, layer.DuckReleaseUS)
		}
		gain := layer.DuckGainDB
		if gain == 0 {
			gain = DefaultBGMDuckGainDB
		}
		attack := layer.DuckAttackUS
		if attack == 0 {
			attack = DefaultBGMDuckAttackUS
		}
		release := layer.DuckReleaseUS
		if release == 0 {
			release = DefaultBGMDuckReleaseUS
		}
		layerEnd := layer.TimelineStartUS + layer.DurationUS
		for _, w := range speech {
			start := max(layer.TimelineStartUS, w.start)
			end := min(layerEnd, w.end)
			if end <= start {
				continue
			}
			out = append(out, audio.AudioAutomation{
				TargetTrackID:  "bgm",
				TriggerTrackID: "voiceover",
				StartUS:        start,
				EndUS:          end,
				GainDB:         gain,
				AttackUS:       attack,
				ReleaseUS:      release,
			})
		}
	}
	return out, nil
}

// voSpeechWindow is one voiceover speech interval in absolute timeline
// coordinates. It ends at min(TimelineDurationUS, SourceDurationUS) so the
// duck zone covers the certified speech length, never the whole scene
// window.
type voSpeechWindow struct {
	start int64
	end   int64
}

// voiceoverSpeechWindows derives every voiceover speech interval from the
// canonical timeline (segment start + intent offset, clamped to the
// certified source duration).
func voiceoverSpeechWindows(timeline audio.CanonicalTimeline) []voSpeechWindow {
	var out []voSpeechWindow
	for _, seg := range timeline.Segments {
		for _, intent := range seg.EffectiveAudioIntents() {
			if intent.Mode != audio.AudioVoiceover {
				continue
			}
			start := seg.TimelineStartUS + intent.TimelineOffsetUS
			windowDur := intent.TimelineDurationUS
			if windowDur <= 0 {
				windowDur = seg.DurationUS - intent.TimelineOffsetUS
			}
			if windowDur <= 0 {
				continue
			}
			speech := intent.SourceDurationUS
			if speech <= 0 || speech > windowDur {
				speech = windowDur
			}
			out = append(out, voSpeechWindow{start: start, end: start + speech})
		}
	}
	return out
}

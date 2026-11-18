// Package clipsequence owns the contract for joining rendered clips into one
// artifact.
//
// # Why a separate contract
//
// clip.render produces ONE artifact per source clip. A delivered video, however,
// is a sequence of those artifacts, and the editorial decision that matters is
// which neighbours are *fused* and which simply follow one another. That
// decision is not a renderer concern: an assembler that concatenates everything
// hides the cut between two clips that were never meant to touch, and one that
// concatenates nothing leaves a visible jump where a transition was requested.
//
// The rule this package encodes is the producer's: two adjacent clips are
// merged into a single artifact ONLY when the incoming clip declares a
// transition. A clip without a transition starts a new artifact, even when the
// previous one ended with a transition. The plan is therefore deterministic and
// independent of how the renderer executes it.
//
// # Renderer capability
//
// Each preset names the Chronon clip-transition kind that renders it (Flash,
// LightLeak, Dissolve, Zoom, Slide) and, when the local ffmpeg assembler can
// reproduce the same look, its xfade program. A preset with no local program is
// not silently degraded: the assembler refuses it and names the renderer that
// owns it (see platform/media/clipmerge).
package clipsequence

import (
	"sort"
)

// Preset is a certified fast transition between two clips.
type Preset string

const (
	// PresetFlashWhite is a white flash that hides the cut.
	PresetFlashWhite Preset = "flash_white"
	// PresetLightLeakSoft is a soft diagonal light-leak sweep.
	PresetLightLeakSoft Preset = "light_leak_soft"
	// PresetLightLeakHard is a stronger, shorter light-leak hit.
	PresetLightLeakHard Preset = "light_leak_hard"
	// PresetShakeLight is a light camera shake across the cut.
	PresetShakeLight Preset = "shake_light"
	// PresetShakeStrong is a harder camera shake across the cut.
	PresetShakeStrong Preset = "shake_strong"
	// PresetWhipLeft whips the frame out to the left.
	PresetWhipLeft Preset = "whip_left"
	// PresetWhipRight whips the frame out to the right.
	PresetWhipRight Preset = "whip_right"
	// PresetZoomPunch punches into the next clip.
	PresetZoomPunch Preset = "zoom_punch"
	// PresetCrossfadeFast is the short editorial dissolve.
	PresetCrossfadeFast Preset = "crossfade_fast"
	// PresetSqueezeIn squeezes the outgoing frame into the incoming one.
	PresetSqueezeIn Preset = "squeeze_in"
)

const (
	// MinTransitionMS is the shortest transition the contract accepts: below
	// this the cut is a hard cut with a glitch frame, not a transition.
	MinTransitionMS int64 = 80
	// MaxFastTransitionMS is the ceiling for this catalogue. Every preset here
	// is a *fast* transition — the editorial lane these presets exist for —
	// so a one-second dissolve is rejected instead of silently accepted.
	MaxFastTransitionMS int64 = 400
	// MaxTransitionFractionPercent caps one transition at 40% of either
	// neighbour. Beyond that the transition swallows the clip and the result
	// reads as neither clip.
	MaxTransitionFractionPercent int64 = 40
)

// PresetSpec is the certified definition of one preset: the renderer kind that
// owns its look and, when it exists, the local assembler program.
type PresetSpec struct {
	Preset Preset
	// ChrononKind is the Chronon ClipTransitionKind that renders this preset.
	// Empty means Chronon has no matching kind yet and the preset is not
	// renderable anywhere.
	ChrononKind string
	// Xfade is the ffmpeg xfade program that reproduces the look locally.
	// Empty means the local assembler cannot render it faithfully and must
	// refuse instead of approximating.
	Xfade string
	// DefaultDurationMS is used when the payload omits the duration.
	DefaultDurationMS int64
	// Description is the editorial intent, for logs and payload docs.
	Description string
}

// presetCatalog is the closed vocabulary. Adding a preset is one entry here,
// never an inline switch elsewhere.
var presetCatalog = map[Preset]PresetSpec{
	PresetFlashWhite: {
		Preset: PresetFlashWhite, ChrononKind: "Flash", Xfade: "fadewhite",
		DefaultDurationMS: 180, Description: "white flash over the cut",
	},
	PresetLightLeakSoft: {
		Preset: PresetLightLeakSoft, ChrononKind: "LightLeak",
		DefaultDurationMS: 260, Description: "soft diagonal light leak",
	},
	PresetLightLeakHard: {
		Preset: PresetLightLeakHard, ChrononKind: "LightLeak",
		DefaultDurationMS: 320, Description: "hard light-leak hit",
	},
	PresetShakeLight: {
		Preset: PresetShakeLight, ChrononKind: "Shake",
		DefaultDurationMS: 200, Description: "light shake across the cut",
	},
	PresetShakeStrong: {
		Preset: PresetShakeStrong, ChrononKind: "Shake",
		DefaultDurationMS: 240, Description: "strong shake across the cut",
	},
	PresetWhipLeft: {
		Preset: PresetWhipLeft, ChrononKind: "Slide", Xfade: "slideleft",
		DefaultDurationMS: 160, Description: "fast whip out to the left",
	},
	PresetWhipRight: {
		Preset: PresetWhipRight, ChrononKind: "Slide", Xfade: "slideright",
		DefaultDurationMS: 160, Description: "fast whip out to the right",
	},
	PresetZoomPunch: {
		Preset: PresetZoomPunch, ChrononKind: "Zoom", Xfade: "zoomin",
		DefaultDurationMS: 180, Description: "punch into the next clip",
	},
	PresetCrossfadeFast: {
		Preset: PresetCrossfadeFast, ChrononKind: "Dissolve", Xfade: "fade",
		DefaultDurationMS: 150, Description: "short editorial dissolve",
	},
	PresetSqueezeIn: {
		Preset: PresetSqueezeIn, ChrononKind: "Zoom", Xfade: "squeezeh",
		DefaultDurationMS: 180, Description: "squeeze the outgoing frame away",
	},
}

// Presets returns every certified preset in stable order.
func Presets() []PresetSpec {
	out := make([]PresetSpec, 0, len(presetCatalog))
	for _, spec := range presetCatalog {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Preset < out[j].Preset })
	return out
}

// LookupPreset returns the certified spec for one preset.
func LookupPreset(preset Preset) (PresetSpec, bool) {
	spec, ok := presetCatalog[preset]
	return spec, ok
}

// LocallyExecutable reports whether the local assembler can render the preset
// faithfully. It never claims a preset the local renderer would approximate.
func (s PresetSpec) LocallyExecutable() bool { return s.Xfade != "" }

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
// reproduce the same look, the program that renders it: an xfade transition for
// the geometric looks, or a sweep specification for the light leaks. A preset
// with no local program is not silently degraded: the assembler refuses it and
// names the renderer that owns it (see platform/media/clipmerge).
package clipsequence

import (
	"fmt"
	"strings"
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
	// is a *fast* transition — the editorial lane these presets exist for — so
	// the contract stops at 300 ms: a longer dissolve belongs to the slow lane
	// and is rejected here instead of silently accepted as a quick cut.
	MaxFastTransitionMS int64 = 300
	// MaxTransitionFractionPercent caps one transition at 40% of either
	// neighbour. Beyond that the transition swallows the clip and the result
	// reads as neither clip.
	MaxTransitionFractionPercent int64 = 40
)

// LeakSpec is the local light-leak program: a warm band that sweeps across the
// frame for the length of the join, peaking at the cut. The filtergraph is
// deterministic (frame time only), so a merge replay renders the same leak.
type LeakSpec struct {
	// Color is the band colour as #RRGGBB.
	Color string
	// PeakAlpha is the band opacity at its centre, in (0,1].
	PeakAlpha float64
	// BandWidth is the normalised half-width of the band, in (0,1]. A small
	// value is a tighter, harder hit; a large one a soft wash.
	BandWidth float64
	// Direction is "right" or "left": the side the band sweeps towards.
	Direction string
}

// LeakOverride parameterises the light-leak program for one join. Every field
// is optional: an omitted field keeps the preset's value, so a caller can tune
// one knob without restating the others.
type LeakOverride struct {
	// Color is the band colour as #RRGGBB.
	Color string `json:"color,omitempty"`
	// PeakAlpha is the band opacity at its centre, in (0,1].
	PeakAlpha *float64 `json:"peak_alpha,omitempty"`
	// BandWidth is the normalised half-width of the band, in (0,1].
	BandWidth *float64 `json:"band_width,omitempty"`
	// Direction is "right" or "left": the side the band sweeps towards.
	Direction string `json:"direction,omitempty"`
}

// ResolveLeak returns the program one join runs: the preset's values with the
// clip's overrides applied. A nil override is the preset itself.
//
// It fails closed on an override that names no light leak (an xfade preset has
// no band to colour) and on any value the assembler cannot render, so a typo in
// a transition is a rejected sequence, not a silently unchanged look.
func (s PresetSpec) ResolveLeak(override *LeakOverride) (*LeakSpec, error) {
	if s.Leak == nil {
		if override != nil {
			return nil, fmt.Errorf("transition preset %q has no light-leak program: leak overrides apply to light leaks only", s.Preset)
		}
		return nil, nil
	}
	resolved := *s.Leak
	if override == nil {
		return &resolved, nil
	}
	if override.Color != "" {
		resolved.Color = strings.ToUpper(strings.TrimSpace(override.Color))
	}
	if override.PeakAlpha != nil {
		resolved.PeakAlpha = *override.PeakAlpha
	}
	if override.BandWidth != nil {
		resolved.BandWidth = *override.BandWidth
	}
	if override.Direction != "" {
		resolved.Direction = strings.ToLower(strings.TrimSpace(override.Direction))
	}
	if err := validateLeak(resolved); err != nil {
		return nil, fmt.Errorf("transition preset %q: %w", s.Preset, err)
	}
	return &resolved, nil
}

// validateLeak is the single range check for a light-leak program. The
// assembler performs the same one at build time; validating here makes a bad
// override a plan error the caller can read before any frame is rendered.
func validateLeak(leak LeakSpec) error {
	if len(leak.Color) != 7 || leak.Color[0] != '#' {
		return fmt.Errorf("leak color %q must be #RRGGBB", leak.Color)
	}
	for _, c := range leak.Color[1:] {
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
			return fmt.Errorf("leak color %q must be #RRGGBB", leak.Color)
		}
	}
	if leak.PeakAlpha <= 0 || leak.PeakAlpha > 1 {
		return fmt.Errorf("leak peak_alpha %v must be in (0,1]", leak.PeakAlpha)
	}
	if leak.BandWidth <= 0 || leak.BandWidth > 1 {
		return fmt.Errorf("leak band_width %v must be in (0,1]", leak.BandWidth)
	}
	if leak.Direction != "right" && leak.Direction != "left" {
		return fmt.Errorf("leak direction %q must be right or left", leak.Direction)
	}
	return nil
}

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
	// Leak is the local light-leak program. It is set when the look is a
	// moving flare rather than an xfade blend; Xfade and Leak are never both
	// set and never both empty for an executable preset.
	Leak *LeakSpec
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
		Leak:              &LeakSpec{Color: "#FFD9A0", PeakAlpha: 0.60, BandWidth: 0.26, Direction: "right"},
		DefaultDurationMS: 260, Description: "soft diagonal light leak",
	},
	PresetLightLeakHard: {
		Preset: PresetLightLeakHard, ChrononKind: "LightLeak",
		Leak:              &LeakSpec{Color: "#FFC070", PeakAlpha: 0.85, BandWidth: 0.16, Direction: "right"},
		DefaultDurationMS: 200, Description: "hard light-leak hit",
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

// LookupPreset returns the certified spec for one preset.
func LookupPreset(preset Preset) (PresetSpec, bool) {
	spec, ok := presetCatalog[preset]
	return spec, ok
}

// LocallyExecutable reports whether the local assembler can render the preset
// faithfully. It never claims a preset the local renderer would approximate.
func (s PresetSpec) LocallyExecutable() bool { return s.Xfade != "" || s.Leak != nil }

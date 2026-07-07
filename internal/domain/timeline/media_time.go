// Package timeline — media_time.go (FASE A, July 2026).
//
// MediaTimeSpec is the separate resolver for source media frames.
// It operates AFTER the sequence time mapping: the flow is
// global_frame → sequence local_frame → media source_frame.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of MediaTimeSpec and resolve_media_frame().
// source_frame MUST NOT be calculated in renderers, video nodes,
// or FFmpeg samplers — those consumers receive a pre-resolved frame.
//
// godlike/07 typed-error contract: playback_rate=0 is rejected as
// ErrMediaPlaybackRateInvalid. Negative rates are valid (reverse
// playback). NaN/Inf rates are rejected.
package timeline

import (
	"fmt"
	"math"
)

// ── MediaTimeSpec ──────────────────────────────────────────────────

// MediaTimeSpec defines how a sequence's local_frame maps to a
// source media frame. It controls trimming, playback rate, and
// freeze behavior independently of the sequence's temporal window.
//
// Zero value: trim_before=0, no trim_after (play to end),
// playback_rate=1.0, no freeze.
type MediaTimeSpec struct {
	// TrimBefore is the number of frames to skip at the start of
	// the source media. source_frame = trim_before + (local_frame * playback_rate).
	TrimBefore Frame `json:"trim_before"`

	// TrimAfter is the frame at which the source media ends.
	// nil means "play to natural end of source".
	TrimAfter *Frame `json:"trim_after,omitempty"`

	// PlaybackRate is the speed multiplier.
	// 1.0 = normal speed, 2.0 = double speed, 0.5 = half speed.
	// Negative values = reverse playback.
	PlaybackRate float64 `json:"playback_rate"`

	// Freeze when true locks the source frame to FreezeAt for
	// all local_frame values.
	Freeze bool `json:"freeze"`

	// FreezeAt is the source frame to freeze on when Freeze is true.
	FreezeAt Frame `json:"freeze_at"`
}

// DefaultMediaTimeSpec returns a MediaTimeSpec with sensible defaults:
// no trim, normal speed (1.0x), no freeze.
func DefaultMediaTimeSpec() MediaTimeSpec {
	return MediaTimeSpec{
		TrimBefore:   0,
		TrimAfter:    nil,
		PlaybackRate: 1.0,
		Freeze:       false,
		FreezeAt:     0,
	}
}

// ── resolve_media_frame ─────────────────────────────────────────────

// ErrMediaPlaybackRateInvalid is the typed sentinel for invalid playback rates.
var ErrMediaPlaybackRateInvalid = fmt.Errorf("timeline: media playback rate must be finite and non-zero")

// ResolveMediaFrame maps a local_frame to a source media frame using
// the MediaTimeSpec. This is the canonical entry point for ALL
// media-frame resolution — renderers and video samplers must receive
// a pre-resolved frame from this function.
//
// Algorithm (per the plan §4.6):
//  1. if freeze → return spec.freeze_at
//  2. source = local_frame * playback_rate (floating-point)
//  3. return spec.trim_before + floor(source)
//
// Returns an error if playback_rate is 0, NaN, or Inf.
func ResolveMediaFrame(localFrame Frame, spec MediaTimeSpec) (Frame, error) {
	if spec.Freeze {
		return spec.FreezeAt, nil
	}

	rate := spec.PlaybackRate
	if rate == 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, fmt.Errorf("%w: got %f", ErrMediaPlaybackRateInvalid, rate)
	}

	source := float64(localFrame.Value()) * rate
	return Frame(int64(spec.TrimBefore.Value()) + int64(math.Floor(source))), nil
}

// TrimAfterValue returns trim_after as a value, or 0 if nil.
func (m MediaTimeSpec) TrimAfterValue() Frame {
	if m.TrimAfter == nil {
		return 0
	}
	return *m.TrimAfter
}

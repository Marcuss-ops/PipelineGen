// Package timeline — animation.go (FASE D, July 2026).
//
// FASE D — Animator local frame: defines the canonical animation
// types that consume AnimationSampleContext instead of raw Frame.
// The migration path is:
//
//	Before: sample(anim, Frame frame)
//	After:  anim.Sample(ctx AnimationSampleContext)
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of Keyframe[T], Animation[T], and the Lerp
// interpolation helpers. No other package may reimplement animation
// sampling that bypasses AnimationSampleContext.
//
// godlike/07: animations MUST use ctx.LocalFrame, NOT ctx.GlobalFrame.
// ctx.GlobalFrame is available for debugging/cache keys only.
package timeline

import (
	"fmt"
	"sort"
)

// ── Keyframe[T] ────────────────────────────────────────────────────

// Keyframe is a single point in an animation track. It associates a
// frame index with a typed value. Keyframes in an Animation must be
// sorted by Frame (ascending) for correct binary search.
type Keyframe[T any] struct {
	// Frame is the frame index at which this keyframe takes effect.
	// For the first keyframe in an Animation, the value is held
	// for all frames ≤ Frame (step behavior before the first key).
	Frame Frame `json:"frame"`

	// Value is the typed payload at this keyframe.
	Value T `json:"value"`
}

// ── Animation[T] ───────────────────────────────────────────────────

// Animation is a typed animation track composed of ordered keyframes.
// It supports step (hold) sampling via SampleStep(), and Lerp
// interpolation for Float64Lerp via the standalone SampleFloat64Lerp()
// function.
//
// The zero value is a valid empty animation (SampleStep returns the zero
// value of T and ErrAnimationEmpty).
type Animation[T any] struct {
	// Keyframes is the ordered list of keyframes. Must be sorted
	// by Frame ascending; use Sort() after adding keyframes out
	// of order.
	Keyframes []Keyframe[T] `json:"keyframes"`
}

// NewAnimation creates an Animation from a slice of keyframes and
// sorts them by Frame ascending.
func NewAnimation[T any](kfs []Keyframe[T]) Animation[T] {
	a := Animation[T]{Keyframes: kfs}
	a.Sort()
	return a
}

// Sort sorts the keyframes by Frame ascending. Call after adding
// keyframes out of order.
func (a *Animation[T]) Sort() {
	sort.Slice(a.Keyframes, func(i, j int) bool {
		return a.Keyframes[i].Frame < a.Keyframes[j].Frame
	})
}

// AddKeyframe appends a keyframe and re-sorts. O(n log n) per append;
// for bulk insertion, append all then call Sort() once.
func (a *Animation[T]) AddKeyframe(kf Keyframe[T]) {
	a.Keyframes = append(a.Keyframes, kf)
	a.Sort()
}

// Len returns the number of keyframes.
func (a Animation[T]) Len() int {
	return len(a.Keyframes)
}

// IsEmpty returns true if the animation has no keyframes.
func (a Animation[T]) IsEmpty() bool {
	return len(a.Keyframes) == 0
}

// HasVaryingKeyframes returns true when the animation has 2+ keyframes
// with potentially different values. A single-keyframe animation holds
// a constant value and is effectively static.
//
// NOTE: This is a structural check (keyframe count), not a value-level
// check. Multi-keyframe animations where all values are identical
// (e.g., FloatKF(0, 100), FloatKF(30, 100)) will be marked as "varying"
// even though they are effectively static. This is conservative — a
// false-positive "varying" is safe; a false-negative "static" would
// silently skip needed recomputation.
func (a Animation[T]) HasVaryingKeyframes() bool {
	return len(a.Keyframes) > 1
}

// ── Sentinel errors ────────────────────────────────────────────────

// ErrAnimationEmpty is returned when Sample() is called on an
// empty Animation (zero keyframes).
var ErrAnimationEmpty = fmt.Errorf("timeline: animation has no keyframes")

// ErrAnimationNoInterpolation is returned when an Animation[T] with
// a non-interpolable type T tries to sample at a frame that has no
// exact-match keyframe during step lookup.
var ErrAnimationNoInterpolation = fmt.Errorf("timeline: animation type does not support interpolation")

// ── Sample (step behavior for any T) ───────────────────────────────

// SampleStep returns the value of the nearest keyframe at or before
// ctx.LocalFrame. This is the canonical step/interpolation for types
// that don't implement Lerp (strings, bools, structs).
//
// Algorithm:
//  1. If empty → zero value + ErrAnimationEmpty
//  2. If ctx.LocalFrame ≤ first keyframe → first keyframe's value
//  3. Binary search for the rightmost keyframe ≤ ctx.LocalFrame
//  4. Return that keyframe's value (hold until next keyframe)
//
// godlike/07: uses ctx.LocalFrame, NOT ctx.GlobalFrame.
func (a Animation[T]) SampleStep(ctx AnimationSampleContext) (T, error) {
	var zero T
	if len(a.Keyframes) == 0 {
		return zero, ErrAnimationEmpty
	}

	frame := ctx.LocalFrame

	// Before or at the first keyframe — hold first value.
	if frame <= a.Keyframes[0].Frame {
		return a.Keyframes[0].Value, nil
	}

	// After the last keyframe — hold last value.
	last := a.Keyframes[len(a.Keyframes)-1]
	if frame >= last.Frame {
		return last.Value, nil
	}

	// Binary search for the rightmost keyframe ≤ frame.
	idx := sort.Search(len(a.Keyframes), func(i int) bool {
		return a.Keyframes[i].Frame > frame
	}) - 1

	if idx < 0 {
		return zero, fmt.Errorf("%w: no keyframe at or before local_frame=%d", ErrAnimationNoInterpolation, frame)
	}
	return a.Keyframes[idx].Value, nil
}

// ── Sample for interpolable types ──────────────────────────────────

// Lerpable is implemented by types that support linear interpolation
// between two values given a fraction t in [0, 1].
type Lerpable interface {
	// Lerp returns the linear interpolation between this value and
	// target at fraction t (0 = this, 1 = target).
	Lerp(target Lerpable, t float64) Lerpable
}

// SampleFloat64Lerp samples the animation using linear interpolation
// between surrounding Float64Lerp keyframes. Unlike the generic
// SampleStep, this function is compile-time-safe: it ONLY works with
// Animation[Float64Lerp] — no runtime type assertions.
//
// Algorithm:
//  1. If empty → zero value + ErrAnimationEmpty
//  2. If ctx.LocalFrame ≤ first keyframe → first value
//  3. If ctx.LocalFrame ≥ last keyframe → last value
//  4. Find surrounding keyframes (prev, next)
//  5. t = (local_frame - prev.frame) / (next.frame - prev.frame)
//  6. Return prev.Value.Lerp(next.Value, t)
//
// godlike/07: the interpolation fraction t is derived from
// ctx.LocalFrame, not global_frame.
func SampleFloat64Lerp(anim Animation[Float64Lerp], ctx AnimationSampleContext) (Float64Lerp, error) {
	if len(anim.Keyframes) == 0 {
		return 0, ErrAnimationEmpty
	}

	frame := ctx.LocalFrame

	// Clamp to bounds.
	if frame <= anim.Keyframes[0].Frame {
		return anim.Keyframes[0].Value, nil
	}
	last := anim.Keyframes[len(anim.Keyframes)-1]
	if frame >= last.Frame {
		return last.Value, nil
	}

	// Find the interval [prev, next] containing frame.
	nextIdx := sort.Search(len(anim.Keyframes), func(i int) bool {
		return anim.Keyframes[i].Frame > frame
	})
	prevIdx := nextIdx - 1

	prev := anim.Keyframes[prevIdx]
	next := anim.Keyframes[nextIdx]

	// Compute interpolation fraction.
	span := float64(next.Frame.Value() - prev.Frame.Value())
	if span == 0 {
		return prev.Value, nil
	}
	t := float64(frame.Value()-prev.Frame.Value()) / span

	result := prev.Value.Lerp(next.Value, t)
	typed, ok := result.(Float64Lerp)
	if !ok {
		return 0, fmt.Errorf("%w: Lerp returned %T, expected Float64Lerp", ErrAnimationNoInterpolation, result)
	}
	return typed, nil
}

// ── Float64Lerp ────────────────────────────────────────────────────

// Float64Lerp is a float64 wrapper that implements Lerpable.
// Use this for opacity, scale, position, and other numeric animations.
type Float64Lerp float64

// Lerp implements Lerpable for float64 values.
func (f Float64Lerp) Lerp(target Lerpable, tf float64) Lerpable {
	t, ok := target.(Float64Lerp)
	if !ok {
		return f
	}
	return Float64Lerp(float64(f) + (float64(t)-float64(f))*tf)
}

// ── Convenience constructors ───────────────────────────────────────

// NewFloat64Animation creates an Animation[Float64Lerp] for numeric
// keyframe animations (opacity, scale, position, rotation, etc.).
func NewFloat64Animation(kfs ...Keyframe[Float64Lerp]) Animation[Float64Lerp] {
	return NewAnimation(kfs)
}

// NewStepAnimation creates an Animation[T] for non-interpolable types
// (strings, bools, enums, structs). Uses step (hold) sampling.
func NewStepAnimation[T any](kfs ...Keyframe[T]) Animation[T] {
	return NewAnimation(kfs)
}

// ── Keyframe constructors ──────────────────────────────────────────

// KF creates a Keyframe[T] with the given frame and value.
// Convenience shorthand for struct literal.
func KF[T any](frame Frame, value T) Keyframe[T] {
	return Keyframe[T]{Frame: frame, Value: value}
}

// FloatKF creates a Keyframe[Float64Lerp] from a frame and float64.
func FloatKF(frame Frame, value float64) Keyframe[Float64Lerp] {
	return Keyframe[Float64Lerp]{Frame: frame, Value: Float64Lerp(value)}
}

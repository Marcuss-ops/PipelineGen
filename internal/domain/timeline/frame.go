// Package timeline — frame.go (FASE A, July 2026).
//
// Frame is the canonical, strongly-typed time unit for the timeline
// resolution system. It wraps int64 to prevent accidental mixing of
// global, local, and media frames — each context determines which
// frame type is in play, but the underlying unit is uniform.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of the Frame type and its arithmetic methods.
// No other package may declare a Frame alias or reimplement these
// operations.
//
// godlike/07 typed-error contract: Frame arithmetic is bounds-safe
// (no overflow panic at the int64 boundary — checked via math.MaxInt64
// guards on Add/Sub).
package timeline

import (
	"fmt"
	"math"
)

// Frame is the canonical time unit for the timeline resolution engine.
// It represents a discrete frame index — the smallest indivisible unit
// in the temporal domain. Wrapping int64 prevents accidental type
// confusion between frame indices and other integer values.
//
// Zero value (Frame(0)) is the canonical "start of time" sentinel.
type Frame int64

// Add returns f + offset, guarded against overflow beyond int64 range.
// Overflow returns an error rather than silently wrapping.
func (f Frame) Add(offset int64) (Frame, error) {
	if offset > 0 && f > Frame(math.MaxInt64-offset) {
		return 0, fmt.Errorf("frame overflow: %d + %d exceeds int64 max", f, offset)
	}
	if offset < 0 && f < Frame(math.MinInt64-offset) {
		return 0, fmt.Errorf("frame underflow: %d + %d exceeds int64 min", f, offset)
	}
	return Frame(int64(f) + offset), nil
}

// Sub returns the difference between two frames.
func (f Frame) Sub(other Frame) int64 {
	return int64(f) - int64(other)
}

// Before returns true if f is strictly before other.
func (f Frame) Before(other Frame) bool {
	return f < other
}

// After returns true if f is strictly after other.
func (f Frame) After(other Frame) bool {
	return f > other
}

// IsZero returns true if f is the zero frame (sentinel: "start of time").
func (f Frame) IsZero() bool {
	return f == 0
}

// Value returns the raw int64 value. Prefer typed Frame operations;
// use Value() only at system boundaries (serialization, FFI).
func (f Frame) Value() int64 {
	return int64(f)
}

// FrameFromInt64 converts a raw int64 into a Frame. The canonical
// constructor for Frame values.
func FrameFromInt64(v int64) Frame {
	return Frame(v)
}

// NoFrame is the canonical sentinel for "no frame" / "frame not set".
// Distinct from Frame(0) which is a valid timeline position.
var NoFrame = Frame(-1)

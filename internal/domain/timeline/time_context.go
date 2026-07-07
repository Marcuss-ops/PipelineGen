// Package timeline — time_context.go (FASE A, July 2026).
//
// TimeContext is the unified temporal context that flows through
// every layer of the resolution pipeline. It carries the 3-level
// time model (global / parent / local) plus metadata (fps, scope_path).
//
// AnimationSampleContext is the subset of TimeContext exposed to
// animators — animations use local_frame by default.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of the TimeContext and AnimationSampleContext
// types. No other package may define these structs or their derived
// constructors.
//
// godlike/07 typed-error contract: NaN/Inf fps values are rejected
// at construction time via validateFPS(); zero-value fps is the
// canonical "not set" sentinel.
package timeline

import (
	"fmt"
	"math"
)

// DefaultFPS is the canonical fallback frame rate when no explicit
// fps is configured. 30.0 is the video standard used by the pipeline.
const DefaultFPS = 30.0

// ── TimeContext ────────────────────────────────────────────────────

// TimeContext is the unified temporal context that threads through
// every layer of the timeline resolution pipeline. It encodes the
// 3-level time model (global → parent → local) plus rendering
// metadata.
//
// Zero value is safe: global_frame=0, parent_frame=0, local_frame=0,
// fps=0 (not set), local_seconds=0.0, scope_path="" (root).
type TimeContext struct {
	// GlobalFrame is the frame index in the final output video.
	// This is the outermost time reference — the "wall clock" of
	// the composition.
	GlobalFrame Frame `json:"global_frame"`

	// ParentFrame is the frame index received from the immediate
	// parent in the hierarchy. For the root context this equals
	// GlobalFrame; for a nested sequence this equals the parent's
	// LocalFrame.
	ParentFrame Frame `json:"parent_frame"`

	// LocalFrame is the frame index within the current sequence.
	// This is the frame that content, animators, and media samplers
	// should consume by default (NOT global_frame).
	LocalFrame Frame `json:"local_frame"`

	// SequenceStart is the frame at which the current sequence
	// begins in its parent's time space.
	SequenceStart Frame `json:"sequence_start"`

	// FPS is the frames-per-second of the current context.
	// Defaults to DefaultFPS (30.0) when not explicitly set.
	FPS float64 `json:"fps"`

	// LocalSeconds is the pre-computed time offset in seconds:
	// local_frame / fps. Useful for animators that need
	// continuous-time interpolation.
	LocalSeconds float64 `json:"local_seconds"`

	// ScopePath is the hierarchical path for this time context.
	// Format: "root/intro/title". Used for debugging, logging,
	// and animation scope disambiguation.
	ScopePath string `json:"scope_path"`
}

// NewTimeContext creates a TimeContext with validated FPS.
// ScopePath is empty by default (root convention: children append
// their names to form the hierarchy).
func NewTimeContext(globalFrame, parentFrame, localFrame Frame, fps float64) (TimeContext, error) {
	if err := validateFPS(fps); err != nil {
		return TimeContext{}, fmt.Errorf("time context: %w", err)
	}
	return TimeContext{
		GlobalFrame:   globalFrame,
		ParentFrame:   parentFrame,
		LocalFrame:    localFrame,
		SequenceStart: 0,
		FPS:           fps,
		LocalSeconds:  float64(localFrame.Value()) / fps,
		ScopePath:     "",
	}, nil
}

// NewRootTimeContext creates a root-level TimeContext where
// global_frame == local_frame (FASE B invariant).
func NewRootTimeContext(globalFrame Frame, fps float64) (TimeContext, error) {
	if err := validateFPS(fps); err != nil {
		return TimeContext{}, fmt.Errorf("root time context: %w", err)
	}
	return TimeContext{
		GlobalFrame:   globalFrame,
		ParentFrame:   globalFrame,
		LocalFrame:    globalFrame,
		SequenceStart: 0,
		FPS:           fps,
		LocalSeconds:  float64(globalFrame.Value()) / fps,
		ScopePath:     "",
	}, nil
}

// ChildContext derives a child TimeContext for a nested sequence.
// The child's local_frame is set from mapped.local_frame, and
// its scope_path appends "/" + name to the parent's path.
// If the parent's scope_path is empty (root), the child's path is
// just the sequence name with no leading "/".
func (tc TimeContext) ChildContext(mapped TimeMappingResult, seqName string) TimeContext {
	scopePath := seqName
	if tc.ScopePath != "" {
		scopePath = tc.ScopePath + "/" + seqName
	}
	child := TimeContext{
		GlobalFrame:   tc.GlobalFrame,
		ParentFrame:   tc.LocalFrame,
		LocalFrame:    mapped.LocalFrame,
		SequenceStart: tc.LocalFrame,
		FPS:           tc.FPS,
		ScopePath:     scopePath,
	}
	if tc.FPS > 0 {
		child.LocalSeconds = float64(mapped.LocalFrame.Value()) / tc.FPS
	}
	return child
}

// ToAnimationSampleContext extracts the subset of fields needed by
// animators (FASE D). Animations use local_frame by default.
func (tc TimeContext) ToAnimationSampleContext() AnimationSampleContext {
	return AnimationSampleContext{
		GlobalFrame: tc.GlobalFrame,
		LocalFrame:  tc.LocalFrame,
		FPS:         tc.FPS,
		ScopePath:   tc.ScopePath,
	}
}

// ── AnimationSampleContext ─────────────────────────────────────────

// AnimationSampleContext is the subset of TimeContext exposed to
// animators. It deliberately omits parent_frame and local_seconds
// — animators should only consume local_frame and fps.
//
// godlike/07: animations use local_frame, NOT global_frame.
type AnimationSampleContext struct {
	// GlobalFrame is available for debugging/cache keys only.
	// Animations MUST NOT key their value on global_frame.
	GlobalFrame Frame `json:"global_frame"`

	// LocalFrame is the canonical frame for animation sampling.
	// Every animator must consume ctx.LocalFrame.
	LocalFrame Frame `json:"local_frame"`

	// FPS is the frames-per-second for rate-dependent animations.
	FPS float64 `json:"fps"`

	// ScopePath is the hierarchical path for scope-disambiguated
	// animations (e.g. "root/intro/logo").
	ScopePath string `json:"scope_path"`
}

// ErrFPSInvalid is the typed sentinel for invalid FPS values.
var ErrFPSInvalid = fmt.Errorf("timeline: fps must be a finite positive number")

func validateFPS(fps float64) error {
	if fps <= 0 || math.IsNaN(fps) || math.IsInf(fps, 0) {
		return fmt.Errorf("%w: got %f", ErrFPSInvalid, fps)
	}
	return nil
}

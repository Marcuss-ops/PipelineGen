// Package timeline — animation_test.go (FASE D, July 2026).
//
// TDD contract tests for the FASE D animation types. Verifies:
// - Step sampling with local_frame
// - Lerp interpolation with local_frame
// - Empty animation behavior
// - Keyframe sorting
// - The godlike/07 invariant: ctx.LocalFrame is used, NOT GlobalFrame
package timeline

import (
	"testing"
)

// ── Step Sampling Tests ────────────────────────────────────────────

func TestAnimationStep_SingleKeyframe(t *testing.T) {
	anim := NewStepAnimation(KF(Frame(10), "active"))
	ctx := AnimationSampleContext{LocalFrame: Frame(5), FPS: 30.0}

	// Before keyframe: hold first value
	val, err := anim.SampleStep(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "active" {
		t.Fatalf("expected 'active' at frame 5 (before first keyframe), got %q", val)
	}

	// At keyframe
	ctx.LocalFrame = Frame(10)
	val, _ = anim.SampleStep(ctx)
	if val != "active" {
		t.Fatalf("expected 'active' at frame 10, got %q", val)
	}

	// After keyframe: hold last value
	ctx.LocalFrame = Frame(100)
	val, _ = anim.SampleStep(ctx)
	if val != "active" {
		t.Fatalf("expected 'active' at frame 100, got %q", val)
	}
}

func TestAnimationStep_UsesLocalFrame(t *testing.T) {
	// godlike/07: SampleStep MUST use ctx.LocalFrame, not ctx.GlobalFrame.
	anim := NewStepAnimation(
		KF(Frame(0), "start"),
		KF(Frame(30), "middle"),
		KF(Frame(60), "end"),
	)

	// ctx.LocalFrame = 30 → should get "middle"
	// ctx.GlobalFrame = 999 → must be ignored
	ctx := AnimationSampleContext{
		LocalFrame:  Frame(30),
		GlobalFrame: Frame(999),
		FPS:         30.0,
	}
	val, err := anim.SampleStep(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "middle" {
		t.Fatalf("expected 'middle' at local_frame=30 (ignoring global_frame=999), got %q", val)
	}
}

func TestAnimationStep_BetweenKeyframes(t *testing.T) {
	anim := NewStepAnimation(
		KF(Frame(0), "intro"),
		KF(Frame(50), "main"),
		KF(Frame(100), "outro"),
	)

	// Frame 25: between 0 and 50 → "intro" (hold)
	ctx := AnimationSampleContext{LocalFrame: Frame(25), FPS: 30.0}
	val, _ := anim.SampleStep(ctx)
	if val != "intro" {
		t.Fatalf("expected 'intro' at frame 25, got %q", val)
	}

	// Frame 75: between 50 and 100 → "main" (hold)
	ctx.LocalFrame = Frame(75)
	val, _ = anim.SampleStep(ctx)
	if val != "main" {
		t.Fatalf("expected 'main' at frame 75, got %q", val)
	}
}

func TestAnimationStep_EmptyAnimation(t *testing.T) {
	anim := Animation[string]{}
	ctx := AnimationSampleContext{LocalFrame: Frame(10), FPS: 30.0}

	_, err := anim.SampleStep(ctx)
	if err != ErrAnimationEmpty {
		t.Fatalf("expected ErrAnimationEmpty, got %v", err)
	}
}

// ── Lerp Sampling Tests ────────────────────────────────────────────

func TestAnimationLerp_ExactKeyframe(t *testing.T) {
	anim := NewFloat64Animation(
		FloatKF(Frame(0), 0.0),
		FloatKF(Frame(20), 1.0),
		FloatKF(Frame(40), 0.0),
	)

	ctx := AnimationSampleContext{LocalFrame: Frame(0), FPS: 30.0}
	val, err := SampleFloat64Lerp(anim, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if float64(val) != 0.0 {
		t.Fatalf("expected 0.0 at frame 0, got %f", val)
	}

	ctx.LocalFrame = Frame(20)
	val, _ = SampleFloat64Lerp(anim, ctx)
	if float64(val) != 1.0 {
		t.Fatalf("expected 1.0 at frame 20, got %f", val)
	}
}

func TestAnimationLerp_Midpoint(t *testing.T) {
	anim := NewFloat64Animation(
		FloatKF(Frame(0), 0.0),
		FloatKF(Frame(10), 1.0),
	)

	ctx := AnimationSampleContext{LocalFrame: Frame(5), FPS: 30.0}
	val, err := SampleFloat64Lerp(anim, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if float64(val) != 0.5 {
		t.Fatalf("expected 0.5 at midpoint frame 5, got %f", val)
	}
}

func TestAnimationLerp_UsesLocalFrame(t *testing.T) {
	// godlike/07: SampleFloat64Lerp MUST use ctx.LocalFrame for interpolation.
	anim := NewFloat64Animation(
		FloatKF(Frame(0), 0.0),
		FloatKF(Frame(30), 1.0),
	)

	// LocalFrame = 15 → t = 0.5 → value = 0.5
	// GlobalFrame = 0 → must be ignored
	ctx := AnimationSampleContext{
		LocalFrame:  Frame(15),
		GlobalFrame: Frame(0),
		FPS:         30.0,
	}
	val, err := SampleFloat64Lerp(anim, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if float64(val) != 0.5 {
		t.Fatalf("expected 0.5 (t=0.5 from local_frame=15), got %f", val)
	}
}

func TestAnimationLerp_Empty(t *testing.T) {
	anim := Animation[Float64Lerp]{}
	ctx := AnimationSampleContext{LocalFrame: Frame(10), FPS: 30.0}

	_, err := SampleFloat64Lerp(anim, ctx)
	if err != ErrAnimationEmpty {
		t.Fatalf("expected ErrAnimationEmpty, got %v", err)
	}
}

func TestAnimationLerp_SingleKeyframe(t *testing.T) {
	anim := NewFloat64Animation(FloatKF(Frame(30), 0.75))

	// Before
	ctx := AnimationSampleContext{LocalFrame: Frame(10), FPS: 30.0}
	val, _ := SampleFloat64Lerp(anim, ctx)
	if float64(val) != 0.75 {
		t.Fatalf("expected 0.75 (single keyframe hold), got %f", val)
	}

	// After
	ctx.LocalFrame = Frame(100)
	val, _ = SampleFloat64Lerp(anim, ctx)
	if float64(val) != 0.75 {
		t.Fatalf("expected 0.75 (single keyframe hold after), got %f", val)
	}
}

// ── Keyframe Sorting Tests ─────────────────────────────────────────

func TestAnimation_SortKeyframes(t *testing.T) {
	anim := NewStepAnimation(
		KF(Frame(50), "c"),
		KF(Frame(10), "a"),
		KF(Frame(30), "b"),
	)

	if anim.Keyframes[0].Frame != 10 {
		t.Fatalf("expected first keyframe at frame 10, got %d", anim.Keyframes[0].Frame)
	}
	if anim.Keyframes[1].Frame != 30 {
		t.Fatalf("expected second keyframe at frame 30, got %d", anim.Keyframes[1].Frame)
	}
	if anim.Keyframes[2].Frame != 50 {
		t.Fatalf("expected third keyframe at frame 50, got %d", anim.Keyframes[2].Frame)
	}
}

// ── Constructor Tests ──────────────────────────────────────────────

func TestAnimation_AddKeyframe(t *testing.T) {
	anim := NewStepAnimation(KF(Frame(10), "a"))
	anim.AddKeyframe(KF(Frame(5), "early"))

	if anim.Keyframes[0].Frame != 5 {
		t.Fatalf("expected first keyframe at frame 5 after add, got %d", anim.Keyframes[0].Frame)
	}
	if anim.Keyframes[0].Value != "early" {
		t.Fatalf("expected 'early' at frame 5, got %q", anim.Keyframes[0].Value)
	}
}

func TestAnimation_IsEmpty(t *testing.T) {
	empty := Animation[string]{}
	if !empty.IsEmpty() {
		t.Fatal("expected IsEmpty()=true for zero-value Animation")
	}

	nonEmpty := NewStepAnimation(KF(Frame(0), "x"))
	if nonEmpty.IsEmpty() {
		t.Fatal("expected IsEmpty()=false for populated Animation")
	}
}

func TestFloat64Lerp_Interpolation(t *testing.T) {
	a := Float64Lerp(0.0)
	b := Float64Lerp(100.0)

	r := a.Lerp(b, 0.25)
	if float64(r.(Float64Lerp)) != 25.0 {
		t.Fatalf("expected 25.0 at t=0.25, got %f", r)
	}

	r = a.Lerp(b, 0.75)
	if float64(r.(Float64Lerp)) != 75.0 {
		t.Fatalf("expected 75.0 at t=0.75, got %f", r)
	}

	r = a.Lerp(b, 1.0)
	if float64(r.(Float64Lerp)) != 100.0 {
		t.Fatalf("expected 100.0 at t=1.0, got %f", r)
	}
}

// ── Convenience Constructors ────────────────────────────────────────

func TestFloatKF(t *testing.T) {
	kf := FloatKF(Frame(30), 0.5)
	if kf.Frame != 30 {
		t.Fatalf("expected Frame=30, got %d", kf.Frame)
	}
	if float64(kf.Value) != 0.5 {
		t.Fatalf("expected Value=0.5, got %f", kf.Value)
	}
}

func TestKF_Generic(t *testing.T) {
	kf := KF(Frame(10), "hello")
	if kf.Frame != 10 {
		t.Fatalf("expected Frame=10, got %d", kf.Frame)
	}
	if kf.Value != "hello" {
		t.Fatalf("expected Value='hello', got %q", kf.Value)
	}
}

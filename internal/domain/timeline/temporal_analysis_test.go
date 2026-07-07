// Package timeline — temporal_analysis_test.go (FASE F, July 2026).
//
// TDD contract tests for the FASE F TemporalAnalysis. Verifies that
// the dependency analysis correctly detects static vs dynamic nodes,
// replacing the dangerous `duration==1` heuristic.
//
// godlike/06 SSOT: these tests are the canonical behavioral contract
// for TemporalAnalysis. A future refactor that changes the detection
// semantics MUST update these tests.
package timeline

import (
	"testing"
)

// ── Static Detection Tests ─────────────────────────────────────────

func TestTemporalAnalysis_StaticLayer_NoAnimations(t *testing.T) {
	layer := LayerNode{
		Name: "static_text",
		Kind: LayerKindText,
		Properties: LayerProperties{
			Text: "hello", // text only, no animations
		},
	}
	ta := AnalyzeTemporalDependencies(layer)

	if !ta.IsStatic() {
		t.Fatal("expected IsStatic()=true for text-only layer with no animations")
	}
	if ta.IsFrameDependent() {
		t.Fatal("expected IsFrameDependent()=false for text-only layer")
	}
	if ta.FrameDependent {
		t.Fatal("expected FrameDependent=false")
	}
	if ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=false for standalone layer")
	}
	if ta.MediaTimeDependent {
		t.Fatal("expected MediaTimeDependent=false for text layer")
	}
}

func TestTemporalAnalysis_DynamicLayer_WithAnimation(t *testing.T) {
	layer := LayerNode{
		Name: "animated_text",
		Kind: LayerKindText,
		Properties: LayerProperties{
			Text: "fading",
			Opacity: NewFloat64Animation(
				FloatKF(Frame(0), 0.0),
				FloatKF(Frame(30), 1.0),
			),
		},
	}
	ta := AnalyzeTemporalDependencies(layer)

	if ta.IsStatic() {
		t.Fatal("expected IsStatic()=false for layer with opacity animation")
	}
	if !ta.FrameDependent {
		t.Fatal("expected FrameDependent=true for animated layer")
	}
}

func TestTemporalAnalysis_MediaNode_MediaTimeDependent(t *testing.T) {
	media := MediaNode{
		Name:       "video",
		SourcePath: "/media/bg.mp4",
		MediaTime:  DefaultMediaTimeSpec(),
	}
	ta := AnalyzeTemporalDependencies(media)

	if ta.IsStatic() {
		t.Fatal("expected IsStatic()=false for media node")
	}
	if !ta.MediaTimeDependent {
		t.Fatal("expected MediaTimeDependent=true for media node")
	}
	if ta.FrameDependent {
		t.Fatal("expected FrameDependent=false for media node")
	}
}

func TestTemporalAnalysis_SequenceNode_LocalTimeDependent(t *testing.T) {
	seq := NewSequence("intro", SequenceSpec{From: Frame(30)})
	seq.AddChild(LayerNode{
		Name:       "static_text",
		Kind:       LayerKindText,
		Properties: LayerProperties{Text: "hello"},
	})

	ta := AnalyzeTemporalDependencies(seq)

	if ta.IsStatic() {
		t.Fatal("expected IsStatic()=false for sequence (local-time-dependent)")
	}
	if !ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=true for sequence")
	}
	// Child is static, so FrameDependent should still be false
	if ta.FrameDependent {
		t.Fatal("expected FrameDependent=false for sequence with static child")
	}
}

func TestTemporalAnalysis_SequenceWithAnimatedChild(t *testing.T) {
	seq := NewSequence("title", SequenceSpec{From: Frame(30)})
	seq.AddChild(LayerNode{
		Name: "fading_text",
		Kind: LayerKindText,
		Properties: LayerProperties{
			Text: "fade",
			Opacity: NewFloat64Animation(
				FloatKF(Frame(0), 0.0),
				FloatKF(Frame(20), 1.0),
			),
		},
	})

	ta := AnalyzeTemporalDependencies(seq)

	if !ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=true for sequence")
	}
	if !ta.FrameDependent {
		t.Fatal("expected FrameDependent=true for sequence with animated child")
	}
	if ta.IsStatic() {
		t.Fatal("expected IsStatic()=false")
	}
}

func TestTemporalAnalysis_SequenceWithMediaChild(t *testing.T) {
	seq := NewSequence("video_clip", SequenceSpec{From: Frame(0)})
	seq.AddChild(MediaNode{
		Name:       "clip",
		SourcePath: "/media/clip.mp4",
		MediaTime:  DefaultMediaTimeSpec(),
	})

	ta := AnalyzeTemporalDependencies(seq)

	if !ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=true")
	}
	if !ta.MediaTimeDependent {
		t.Fatal("expected MediaTimeDependent=true for sequence with media child")
	}
}

func TestTemporalAnalysis_AllAnimationProperties(t *testing.T) {
	layer := LayerNode{
		Name: "full_anim",
		Kind: LayerKindShape,
		Properties: LayerProperties{
			Scale: NewFloat64Animation(
				FloatKF(Frame(0), 1.0),
				FloatKF(Frame(30), 2.0),
			),
			PositionX: NewFloat64Animation(
				FloatKF(Frame(0), 0),
				FloatKF(Frame(30), 100),
			),
			PositionY: NewFloat64Animation(
				FloatKF(Frame(0), 50),
				FloatKF(Frame(30), 200),
			),
			Rotation: NewFloat64Animation(
				FloatKF(Frame(0), 0),
				FloatKF(Frame(30), 360),
			),
		},
	}
	ta := AnalyzeTemporalDependencies(layer)

	if !ta.FrameDependent {
		t.Fatal("expected FrameDependent=true for layer with multi-keyframe animations")
	}
}

func TestTemporalAnalysis_AnalyzeComposition(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		// Static sequence
		c.Sequence("bg", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("bg_text", LayerKindText, func(l *LayerBuilder) {
				l.WithText("BACKGROUND")
			})
		})
		// Animated sequence
		c.Sequence("title", SequenceSpec{From: Frame(30), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("text", LayerKindText, func(l *LayerBuilder) {
				l.WithText("TITLE")
				l.WithOpacityAnim(
					FloatKF(Frame(0), 0.0),
					FloatKF(Frame(20), 1.0),
				)
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ta := AnalyzeComposition(comp)

	if !ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=true (composition with sequences)")
	}
	if !ta.FrameDependent {
		t.Fatal("expected FrameDependent=true (composition has animated layer)")
	}
	if ta.IsStatic() {
		t.Fatal("expected IsStatic()=false for composition with animations")
	}
}

func TestTemporalAnalysis_FullyStaticComposition(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("static", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("text", LayerKindText, func(l *LayerBuilder) {
				l.WithText("STATIC")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ta := AnalyzeComposition(comp)

	if ta.FrameDependent {
		t.Fatal("expected FrameDependent=false (no animations)")
	}
	if !ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=true (has sequences)")
	}
	if ta.MediaTimeDependent {
		t.Fatal("expected MediaTimeDependent=false (no media nodes)")
	}
	if ta.IsStatic() {
		t.Fatal("expected IsStatic()=false (has sequences, which are local-time-dependent)")
	}
}

func TestTemporalAnalysis_SingleKeyframeAnimation_IsStatic(t *testing.T) {
	// A single keyframe holds a constant value → NOT frame-dependent.
	layer := LayerNode{
		Name: "static_opacity",
		Kind: LayerKindText,
		Properties: LayerProperties{
			Text: "always visible",
			Opacity: NewFloat64Animation(
				FloatKF(Frame(0), 0.8),
			),
		},
	}
	ta := AnalyzeTemporalDependencies(layer)

	if ta.FrameDependent {
		t.Fatal("expected FrameDependent=false for single-keyframe animation (static hold)")
	}
	if !ta.IsStatic() {
		t.Fatal("expected IsStatic()=true for single-keyframe animation")
	}
}

// ── Zero-value / edge-case tests ───────────────────────────────────

func TestTemporalAnalysis_ZeroValue(t *testing.T) {
	ta := TemporalAnalysis{}
	if !ta.IsStatic() {
		t.Fatal("expected IsStatic()=true for zero-value analysis")
	}
	if ta.IsFrameDependent() {
		t.Fatal("expected IsFrameDependent()=false for zero-value")
	}
}

func TestTemporalAnalysis_EmptySequence(t *testing.T) {
	seq := NewSequence("empty", SequenceSpec{From: Frame(10)})
	ta := AnalyzeTemporalDependencies(seq)

	if !ta.LocalTimeDependent {
		t.Fatal("expected LocalTimeDependent=true even for empty sequence")
	}
	if ta.FrameDependent || ta.MediaTimeDependent {
		t.Fatal("expected FrameDependent=false, MediaTimeDependent=false for empty sequence")
	}
}

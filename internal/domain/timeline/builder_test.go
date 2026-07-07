// Package timeline — builder_test.go (FASE E, July 2026).
//
// TDD contract tests for the FASE E builder API. Verifies the fluent
// construction of Compositions with sequences, layers, nested sequences,
// animations, and round-trip resolution through ResolveCompositionFlat.
//
// godlike/06 SSOT: these tests are the canonical behavioral contract
// for the builder API. A future refactor that changes the builder
// semantics MUST update these tests.
package timeline

import (
	"testing"
)

// ── BuildComposition Tests ─────────────────────────────────────────

func TestBuildComposition_BasicSequenceLayer(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("intro", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("logo", LayerKindText, func(l *LayerBuilder) {
				l.WithText("INTRO")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if comp.RootSequence.ChildCount() != 1 {
		t.Fatalf("expected 1 child in root, got %d", comp.RootSequence.ChildCount())
	}

	seq, ok := comp.RootSequence.Children[0].(SequenceNode)
	if !ok {
		t.Fatalf("expected SequenceNode, got %T", comp.RootSequence.Children[0])
	}
	if seq.Name != "intro" {
		t.Fatalf("expected sequence name 'intro', got %q", seq.Name)
	}
	if seq.Spec.From != 0 {
		t.Fatalf("expected From=0, got %d", seq.Spec.From)
	}

	seqChild := seq.Children[0].(LayerNode)
	props, ok := seqChild.Properties.(LayerProperties)
	if !ok {
		t.Fatalf("expected LayerProperties, got %T", seqChild.Properties)
	}
	if props.Text != "INTRO" {
		t.Fatalf("expected text 'INTRO', got %q", props.Text)
	}
}

func TestBuildComposition_MultipleSequences(t *testing.T) {
	dur30 := Frame(30)
	dur60 := Frame(60)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("intro", SequenceSpec{From: Frame(0), Duration: &dur30}, func(s *SequenceBuilder) {
			s.Layer("logo", LayerKindText, func(l *LayerBuilder) {
				l.WithText("INTRO")
			})
		})
		c.Sequence("title", SequenceSpec{From: Frame(30), Duration: &dur60}, func(s *SequenceBuilder) {
			s.Layer("text", LayerKindText, func(l *LayerBuilder) {
				l.WithText("TITLE")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if comp.RootSequence.ChildCount() != 2 {
		t.Fatalf("expected 2 children in root, got %d", comp.RootSequence.ChildCount())
	}
}

func TestBuildComposition_NestedSequences(t *testing.T) {
	comp, err := BuildComposition("test", Frame(500), 30.0, func(c *CompositionBuilder) {
		c.Sequence("chapter", SequenceSpec{From: Frame(100)}, func(chapter *SequenceBuilder) {
			dur := Frame(40)
			chapter.Sequence("title", SequenceSpec{From: Frame(20), Duration: &dur}, func(title *SequenceBuilder) {
				title.Layer("text", LayerKindText, func(l *LayerBuilder) {
					l.WithText("NESTED")
				})
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// chapter sequence should have 1 child (title)
	chapterSeq := comp.RootSequence.Children[0].(SequenceNode)
	if chapterSeq.ChildCount() != 1 {
		t.Fatalf("expected chapter to have 1 child (title), got %d", chapterSeq.ChildCount())
	}

	// title sequence should have 1 child (text layer)
	titleSeq := chapterSeq.Children[0].(SequenceNode)
	if titleSeq.ChildCount() != 1 {
		t.Fatalf("expected title to have 1 child (text), got %d", titleSeq.ChildCount())
	}
}

func TestBuildComposition_WithOpacityAnimation(t *testing.T) {
	dur := Frame(20)
	comp, err := BuildComposition("test", Frame(100), 30.0, func(c *CompositionBuilder) {
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

	layerNode := comp.RootSequence.Children[0].(SequenceNode).Children[0].(LayerNode)
	props := layerNode.Properties.(LayerProperties)

	if props.Opacity.IsEmpty() {
		t.Fatal("expected non-empty opacity animation")
	}
	if props.Opacity.Len() != 2 {
		t.Fatalf("expected 2 opacity keyframes, got %d", props.Opacity.Len())
	}
}

func TestBuildComposition_WithPositionAnimation(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(100), 30.0, func(c *CompositionBuilder) {
		c.Sequence("move", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("box", LayerKindShape, func(l *LayerBuilder) {
				l.WithPositionAnim(
					[]Keyframe[Float64Lerp]{FloatKF(Frame(0), 0), FloatKF(Frame(30), 100)},
					[]Keyframe[Float64Lerp]{FloatKF(Frame(0), 50), FloatKF(Frame(30), 200)},
				)
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	layerNode := comp.RootSequence.Children[0].(SequenceNode).Children[0].(LayerNode)
	props := layerNode.Properties.(LayerProperties)

	if props.PositionX.Len() != 2 {
		t.Fatalf("expected 2 position_x keyframes, got %d", props.PositionX.Len())
	}
	if props.PositionY.Len() != 2 {
		t.Fatalf("expected 2 position_y keyframes, got %d", props.PositionY.Len())
	}
}

func TestBuildComposition_MediaNode(t *testing.T) {
	dur := Frame(60)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("bg", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Media("video", "/media/bg.mp4", DefaultMediaTimeSpec())
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seq := comp.RootSequence.Children[0].(SequenceNode)
	media, ok := seq.Children[0].(MediaNode)
	if !ok {
		t.Fatalf("expected MediaNode, got %T", seq.Children[0])
	}
	if media.SourcePath != "/media/bg.mp4" {
		t.Fatalf("expected SourcePath='/media/bg.mp4', got %q", media.SourcePath)
	}
}

func TestBuildComposition_ScaleAndRotation(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(100), 30.0, func(c *CompositionBuilder) {
		c.Sequence("zoom", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("img", LayerKindImage, func(l *LayerBuilder) {
				l.WithScaleAnim(
					FloatKF(Frame(0), 1.0),
					FloatKF(Frame(30), 2.0),
				)
				l.WithRotationAnim(
					FloatKF(Frame(0), 0),
					FloatKF(Frame(30), 360),
				)
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	layerNode := comp.RootSequence.Children[0].(SequenceNode).Children[0].(LayerNode)
	props := layerNode.Properties.(LayerProperties)

	if props.Scale.Len() != 2 {
		t.Fatalf("expected 2 scale keyframes, got %d", props.Scale.Len())
	}
	if props.Rotation.Len() != 2 {
		t.Fatalf("expected 2 rotation keyframes, got %d", props.Rotation.Len())
	}
}

func TestBuildComposition_WithSource(t *testing.T) {
	dur := Frame(60)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("clip", SequenceSpec{From: Frame(0), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("video", LayerKindVideo, func(l *LayerBuilder) {
				l.WithSource("/media/clip.mp4")
				l.WithAssetID("asset-123")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	layerNode := comp.RootSequence.Children[0].(SequenceNode).Children[0].(LayerNode)
	props := layerNode.Properties.(LayerProperties)

	if props.Source != "/media/clip.mp4" {
		t.Fatalf("expected Source='/media/clip.mp4', got %q", props.Source)
	}
	if props.AssetID != "asset-123" {
		t.Fatalf("expected AssetID='asset-123', got %q", props.AssetID)
	}
}

// ── Round-trip Resolution Tests ────────────────────────────────────

func TestBuildComposition_RoundTrip_BeforeSequence(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("intro", SequenceSpec{From: Frame(30), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("logo", LayerKindText, func(l *LayerBuilder) {
				l.WithText("INTRO")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Frame 29: before sequence → not active
	scene := ResolveCompositionFlat(comp, Frame(29))
	if scene.ActiveCount != 0 {
		t.Fatalf("frame 29: expected 0 active layers (before sequence), got %d", scene.ActiveCount)
	}
}

func TestBuildComposition_RoundTrip_DuringSequence(t *testing.T) {
	dur := Frame(30)
	comp, err := BuildComposition("test", Frame(200), 30.0, func(c *CompositionBuilder) {
		c.Sequence("intro", SequenceSpec{From: Frame(30), Duration: &dur}, func(s *SequenceBuilder) {
			s.Layer("logo", LayerKindText, func(l *LayerBuilder) {
				l.WithText("INTRO")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Frame 30: at sequence start, local_frame=0
	scene := ResolveCompositionFlat(comp, Frame(30))
	if scene.ActiveCount != 1 {
		t.Fatalf("frame 30: expected 1 active layer, got %d", scene.ActiveCount)
	}
	if scene.Layers[0].TimeContext.LocalFrame != 0 {
		t.Fatalf("frame 30: expected local_frame=0, got %d", scene.Layers[0].TimeContext.LocalFrame)
	}
}

func TestBuildComposition_RoundTrip_EmptyCallback(t *testing.T) {
	comp, err := BuildComposition("test", Frame(100), 30.0, func(c *CompositionBuilder) {
		// No sequences added — empty composition
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scene := ResolveCompositionFlat(comp, Frame(50))
	if scene.ActiveCount != 0 {
		t.Fatalf("expected 0 active layers for empty composition, got %d", scene.ActiveCount)
	}
}

func TestBuildComposition_RoundTrip_InfiniteDuration(t *testing.T) {
	comp, err := BuildComposition("test", Frame(500), 30.0, func(c *CompositionBuilder) {
		c.Sequence("bg", SequenceSpec{From: Frame(100)}, func(s *SequenceBuilder) {
			s.Layer("bg_layer", LayerKindImage, func(l *LayerBuilder) {
				l.WithText("BACKGROUND")
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Active at frames 100, 200, 499 (infinite duration)
	for _, f := range []int64{100, 200, 499} {
		scene := ResolveCompositionFlat(comp, Frame(f))
		if scene.ActiveCount != 1 {
			t.Fatalf("frame %d: expected 1 active layer (infinite), got %d", f, scene.ActiveCount)
		}
	}
}

// ── PtrFrame convenience test ──────────────────────────────────────

func TestPtrFrame(t *testing.T) {
	p := PtrFrame(Frame(42))
	if p == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *p != 42 {
		t.Fatalf("expected 42, got %d", *p)
	}
}

// ── Round-trip: Nested Sequences ──────────────────────────────────

func TestBuildComposition_RoundTrip_NestedSequences(t *testing.T) {
	dur := Frame(40)
	comp, err := BuildComposition("test", Frame(500), 30.0, func(c *CompositionBuilder) {
		c.Sequence("chapter", SequenceSpec{From: Frame(100)}, func(chapter *SequenceBuilder) {
			chapter.Sequence("title", SequenceSpec{From: Frame(20), Duration: &dur}, func(title *SequenceBuilder) {
				title.Layer("text", LayerKindText, func(l *LayerBuilder) {
					l.WithText("NESTED")
				})
			})
		})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// global_frame = 120: chapter local = 20, title local = 0 → active
	scene := ResolveCompositionFlat(comp, Frame(120))
	if scene.ActiveCount != 1 {
		t.Fatalf("frame 120: expected 1 active nested layer, got %d", scene.ActiveCount)
	}
	if scene.Layers[0].TimeContext.LocalFrame != 0 {
		t.Fatalf("frame 120: expected nested layer local_frame=0, got %d", scene.Layers[0].TimeContext.LocalFrame)
	}

	// global_frame = 100: chapter local = 0, title from=20 → not active yet
	scene100 := ResolveCompositionFlat(comp, Frame(100))
	if scene100.ActiveCount != 0 {
		t.Fatalf("frame 100: expected 0 active (title not active yet), got %d", scene100.ActiveCount)
	}

	// global_frame = 145: chapter local=45, title local=25 → active
	scene145 := ResolveCompositionFlat(comp, Frame(145))
	if scene145.ActiveCount != 1 {
		t.Fatalf("frame 145: expected 1 active, got %d", scene145.ActiveCount)
	}
	if scene145.Layers[0].TimeContext.LocalFrame != 25 {
		t.Fatalf("frame 145: expected local_frame=25, got %d", scene145.Layers[0].TimeContext.LocalFrame)
	}
}

// ── Error propagation ─────────────────────────────────────────────

func TestBuildComposition_InvalidDuration(t *testing.T) {
	_, err := BuildComposition("test", Frame(0), 30.0, func(c *CompositionBuilder) {})
	if err == nil {
		t.Fatal("expected error for duration=0")
	}
}

func TestBuildComposition_InvalidFPS(t *testing.T) {
	_, err := BuildComposition("test", Frame(100), -1, func(c *CompositionBuilder) {})
	if err == nil {
		t.Fatal("expected error for negative FPS")
	}
}

// Package timeline — timeline_test.go (FASE A+B, July 2026).
//
// TDD contract tests for the timeline resolution engine.
// Covers the 9 mandatory test cases from the action plan §6,
// plus additional edge cases for the Frame type, TimeContext,
// Composition, and recursive resolution.
//
// godlike/06 SSOT: the 9 mandatory tests are the canonical behavioral
// contract. A future implementation MUST pass all 9 before the FASE A+B
// wave flips to shipped.
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts exact frame values
// (no tolerance, no "approximately equal"), and every NOT-active case
// asserts Active=false explicitly.
package timeline

import (
	"testing"
)

// ── Mandatory Test 1: global 29 → sequence from 30 → NOT active ──

func TestMapSequenceTime_Global29_SequenceFrom30_NotActive(t *testing.T) {
	spec := SequenceSpec{From: Frame(30)}
	result := MapSequenceTime(spec, Frame(29))

	if result.Active {
		t.Fatalf("expected NOT active at frame 29 with from=30, got Active=true, local=%d", result.LocalFrame)
	}
}

// ── Mandatory Test 2: global 30 → local 0 ──

func TestMapSequenceTime_Global30_Local0(t *testing.T) {
	spec := SequenceSpec{From: Frame(30)}
	result := MapSequenceTime(spec, Frame(30))

	if !result.Active {
		t.Fatalf("expected ACTIVE at frame 30 with from=30")
	}
	if result.LocalFrame != 0 {
		t.Fatalf("expected local_frame=0, got %d", result.LocalFrame)
	}
}

// ── Mandatory Test 3: global 50 → local 20 ──

func TestMapSequenceTime_Global50_Local20(t *testing.T) {
	spec := SequenceSpec{From: Frame(30)}
	result := MapSequenceTime(spec, Frame(50))

	if !result.Active {
		t.Fatalf("expected ACTIVE at frame 50 with from=30")
	}
	if result.LocalFrame != 20 {
		t.Fatalf("expected local_frame=20, got %d", result.LocalFrame)
	}
}

// ── Mandatory Test 4: sequence duration 60 → global 90 → NOT active ──

func TestMapSequenceTime_Duration60_Global90_NotActive(t *testing.T) {
	dur := Frame(60)
	spec := SequenceSpec{From: Frame(30), Duration: &dur}
	result := MapSequenceTime(spec, Frame(90))

	if result.Active {
		t.Fatalf("expected NOT active at frame 90 with from=30 duration=60 (window ends at 89)")
	}

	// Frame 89 should still be active (from + duration - 1)
	result89 := MapSequenceTime(spec, Frame(89))
	if !result89.Active {
		t.Fatalf("expected ACTIVE at frame 89 with from=30 duration=60")
	}
}

// ── Mandatory Test 5: nested sequence → local frame correct ──

func TestNestedSequence_LocalFrameCorrect(t *testing.T) {
	dur := Frame(40)
	innerSeq := NewSequence("title", SequenceSpec{
		From:     Frame(20),
		Duration: &dur,
	})
	innerSeq.AddChild(LayerNode{Name: "text", Kind: LayerKindText})

	comp, err := NewComposition("test-nested", Frame(200), 30.0)
	if err != nil {
		t.Fatalf("unexpected error creating composition: %v", err)
	}
	comp.AddToRoot(NewSequence("chapter", SequenceSpec{
		From: Frame(100),
	}))
	// Add inner to chapter
	chapterSeq := comp.RootSequence.Children[0].(SequenceNode)
	chapterSeq.AddChild(innerSeq)
	comp.RootSequence.Children[0] = chapterSeq

	// global_frame = 120
	// chapter local = 120 - 100 = 20
	// title from = 20 → local = 0
	scene := ResolveCompositionFlat(comp, Frame(120))

	if scene.ActiveCount != 1 {
		t.Fatalf("expected 1 active layer at global 120, got %d (layers: %+v)", scene.ActiveCount, scene.Layers)
	}

	if scene.Layers[0].TimeContext.LocalFrame != 0 {
		t.Fatalf("expected nested layer local_frame=0, got %d (scope=%s)",
			scene.Layers[0].TimeContext.LocalFrame,
			scene.Layers[0].TimeContext.ScopePath)
	}

	if scene.Layers[0].TimeContext.ScopePath != "root/chapter/title" {
		t.Fatalf("expected scope_path=root/chapter/title, got %s",
			scene.Layers[0].TimeContext.ScopePath)
	}
}

// ── Mandatory Test 6: anim inside sequence starts from local 0 ──

func TestAnimationSampleContext_LocalFrameStartsFromZero(t *testing.T) {
	spec := SequenceSpec{From: Frame(30)}
	result := MapSequenceTime(spec, Frame(30))

	timeCtx := TimeContext{
		GlobalFrame: 30,
		LocalFrame:  result.LocalFrame,
		FPS:         30.0,
		ScopePath:   "root/intro",
	}
	animCtx := timeCtx.ToAnimationSampleContext()

	if animCtx.LocalFrame != 0 {
		t.Fatalf("expected animation local_frame=0 at sequence start, got %d", animCtx.LocalFrame)
	}
	if animCtx.GlobalFrame != 30 {
		t.Fatalf("expected animation global_frame=30 (for debug only), got %d", animCtx.GlobalFrame)
	}
}

// ── Mandatory Test 7: trim_before 10 → local 0 becomes media/source 10 ──

func TestResolveMediaFrame_TrimBefore10(t *testing.T) {
	spec := MediaTimeSpec{
		TrimBefore:   10,
		PlaybackRate: 1.0,
	}

	sourceFrame, err := ResolveMediaFrame(Frame(0), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sourceFrame != 10 {
		t.Fatalf("expected source_frame=10 with trim_before=10 and local_frame=0, got %d", sourceFrame)
	}

	// local_frame=5 → source_frame=15
	sourceFrame, _ = ResolveMediaFrame(Frame(5), spec)
	if sourceFrame != 15 {
		t.Fatalf("expected source_frame=15 with trim_before=10 and local_frame=5, got %d", sourceFrame)
	}
}

// ── Mandatory Test 8: freeze_at 15 → all frames use 15 ──

func TestResolveMediaFrame_FreezeAt15(t *testing.T) {
	spec := MediaTimeSpec{
		Freeze:   true,
		FreezeAt: Frame(15),
	}

	for _, localFrame := range []Frame{0, 5, 10, 100, 999} {
		sourceFrame, err := ResolveMediaFrame(localFrame, spec)
		if err != nil {
			t.Fatalf("unexpected error at local_frame=%d: %v", localFrame, err)
		}
		if sourceFrame != 15 {
			t.Fatalf("expected source_frame=15 (frozen) at local_frame=%d, got %d", localFrame, sourceFrame)
		}
	}
}

// ── Mandatory Test 9: playback_rate 2.0 → local 10 uses source 20 ──

func TestResolveMediaFrame_PlaybackRate2x(t *testing.T) {
	spec := MediaTimeSpec{
		PlaybackRate: 2.0,
	}

	sourceFrame, err := ResolveMediaFrame(Frame(10), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sourceFrame != 20 {
		t.Fatalf("expected source_frame=20 with playback_rate=2.0 and local_frame=10, got %d", sourceFrame)
	}

	// local_frame=0 → source_frame=0
	sourceFrame, _ = ResolveMediaFrame(Frame(0), spec)
	if sourceFrame != 0 {
		t.Fatalf("expected source_frame=0, got %d", sourceFrame)
	}

	// local_frame=3 → floor(3*2.0)=6 → source_frame=6
	sourceFrame, _ = ResolveMediaFrame(Frame(3), spec)
	if sourceFrame != 6 {
		t.Fatalf("expected source_frame=6 with playback_rate=2.0 and local_frame=3, got %d", sourceFrame)
	}
}

// ── Additional Tests ──────────────────────────────────────────────

// TestFrame_Arithmetic verifies Frame math operations.
func TestFrame_Arithmetic(t *testing.T) {
	a := Frame(10)
	b := Frame(5)

	if a.Sub(b) != 5 {
		t.Fatalf("10 - 5 = %d, expected 5", a.Sub(b))
	}

	sum, err := a.Add(5)
	if err != nil {
		t.Fatalf("unexpected overflow: %v", err)
	}
	if sum != 15 {
		t.Fatalf("10 + 5 = %d, expected 15", sum)
	}
}

// TestFrame_Overflow verifies overflow guards.
func TestFrame_Overflow(t *testing.T) {
	max := Frame(9223372036854775807) // math.MaxInt64
	_, err := max.Add(1)
	if err == nil {
		t.Fatal("expected overflow error on MaxInt64 + 1")
	}
}

// TestTimeContext_ChildContext verifies scope_path construction.
func TestTimeContext_ChildContext(t *testing.T) {
	parent, err := NewRootTimeContext(Frame(50), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mapped := TimeMappingResult{Active: true, LocalFrame: Frame(20)}
	child := parent.ChildContext(mapped, "intro")

	if child.ScopePath != "intro" {
		t.Fatalf("expected scope_path=intro (parent root scope is empty), got %s", child.ScopePath)
	}
	if child.LocalFrame != 20 {
		t.Fatalf("expected local_frame=20, got %d", child.LocalFrame)
	}
	if child.GlobalFrame != 50 {
		t.Fatalf("expected global_frame=50 (inherited), got %d", child.GlobalFrame)
	}
}

// TestTimeContext_FPSInvalid rejects bad FPS values.
func TestTimeContext_FPSInvalid(t *testing.T) {
	_, err := NewRootTimeContext(Frame(0), 0)
	if err == nil {
		t.Fatal("expected error for FPS=0")
	}

	_, err = NewRootTimeContext(Frame(0), -1)
	if err == nil {
		t.Fatal("expected error for FPS=-1")
	}
}

// TestResolveMediaFrame_InvalidRate rejects bad playback rates.
func TestResolveMediaFrame_InvalidRate(t *testing.T) {
	_, err := ResolveMediaFrame(Frame(0), MediaTimeSpec{PlaybackRate: 0})
	if err == nil {
		t.Fatal("expected error for playback_rate=0")
	}
}

// TestNewComposition_RejectsBadDuration rejects zero/negative duration.
func TestNewComposition_RejectsBadDuration(t *testing.T) {
	_, err := NewComposition("test", Frame(0), 30.0)
	if err == nil {
		t.Fatal("expected error for duration=0")
	}

	_, err = NewComposition("test", Frame(-1), 30.0)
	if err == nil {
		t.Fatal("expected error for duration=-1")
	}
}

// TestNewComposition_CreatesRootSequence verifies FASE B root sequence.
func TestNewComposition_CreatesRootSequence(t *testing.T) {
	comp, err := NewComposition("test", Frame(100), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if comp.RootSequence.Name != "root" {
		t.Fatalf("expected root sequence name=root, got %s", comp.RootSequence.Name)
	}
	if comp.RootSequence.Spec.From != 0 {
		t.Fatalf("expected root from=0, got %d", comp.RootSequence.Spec.From)
	}
	if comp.RootSequence.Spec.Duration == nil {
		t.Fatal("expected root duration to be set")
	}
	if *comp.RootSequence.Spec.Duration != 100 {
		t.Fatalf("expected root duration=100, got %d", *comp.RootSequence.Spec.Duration)
	}
}

// TestMapSequenceTime_TrimBefore verifies the trim_before offset.
func TestMapSequenceTime_TrimBefore(t *testing.T) {
	spec := SequenceSpec{
		From:       Frame(30),
		TrimBefore: Frame(10),
	}
	result := MapSequenceTime(spec, Frame(30))

	if !result.Active {
		t.Fatal("expected ACTIVE at frame 30")
	}
	if result.LocalFrame != 10 {
		t.Fatalf("expected local_frame=10 (from=30 → raw=0 + trim_before=10), got %d", result.LocalFrame)
	}
}

// TestMapSequenceTime_Freeze verifies the freeze behavior.
func TestMapSequenceTime_Freeze(t *testing.T) {
	spec := SequenceSpec{
		From:     Frame(30),
		Freeze:   true,
		FreezeAt: Frame(15),
	}

	result30 := MapSequenceTime(spec, Frame(30))
	result60 := MapSequenceTime(spec, Frame(60))

	if !result30.Active || result30.LocalFrame != 15 {
		t.Fatalf("expected local_frame=15 (frozen) at global 30, got %d", result30.LocalFrame)
	}
	if !result60.Active || result60.LocalFrame != 15 {
		t.Fatalf("expected local_frame=15 (frozen) at global 60, got %d", result60.LocalFrame)
	}
}

// TestMapSequenceTime_TrimAfter verifies trim_after gates.
func TestMapSequenceTime_TrimAfter(t *testing.T) {
	trimAfter := Frame(50)
	spec := SequenceSpec{
		From:      Frame(0),
		TrimAfter: &trimAfter,
	}

	// frame 49 should be active
	result49 := MapSequenceTime(spec, Frame(49))
	if !result49.Active {
		t.Fatal("expected ACTIVE at frame 49 with trim_after=50")
	}

	// frame 50 should NOT be active
	result50 := MapSequenceTime(spec, Frame(50))
	if result50.Active {
		t.Fatal("expected NOT active at frame 50 with trim_after=50")
	}
}

// TestEmptyComposition_ProducesEmptyScene verifies empty root sequence.
func TestEmptyComposition_ProducesEmptyScene(t *testing.T) {
	comp, err := NewComposition("empty", Frame(100), 30.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scene := ResolveCompositionFlat(comp, Frame(50))
	if scene.ActiveCount != 0 {
		t.Fatalf("expected 0 active layers for empty composition, got %d", scene.ActiveCount)
	}
	if !scene.IsEmpty() {
		t.Fatal("expected IsEmpty()=true for empty composition")
	}
}

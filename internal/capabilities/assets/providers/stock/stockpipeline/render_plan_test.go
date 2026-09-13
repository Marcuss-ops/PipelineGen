package stockpipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRenderPlanSelectsExplicitTransitionsAndEffectPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.mp4", "a.mp4", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("effect"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := ResolveRenderPlan(RenderRequest{
		InputPaths: []string{"one.mp4", "two.mp4", "three.mp4", "four.mp4"}, TransitionEvery: 2,
		EffectsDir: dir, EffectEvery: 2, EffectIndexHint: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Transitions) != 3 || resolved.Transitions[0].ID != "fadeblack" || resolved.Transitions[0].Segment != "end" {
		t.Fatalf("unexpected transitions: %+v", resolved.Transitions)
	}
	if len(resolved.EffectPaths) != 2 || resolved.EffectPaths[0].Path != filepath.Join(dir, "b.mp4") {
		t.Fatalf("unexpected effect paths: %+v", resolved.EffectPaths)
	}
}

// TestResolveRenderPlanSingleInputHasNoAutoTransitions pins that a single
// input clip never manufactures a transition: a transition is an effect at a
// boundary BETWEEN two clips, and the canonical render_stock plan cannot
// express one. The default stock compose call renders one cut clip, so this
// keeps its request plan-expressible instead of failing closed in Rust.
func TestResolveRenderPlanSingleInputHasNoAutoTransitions(t *testing.T) {
	resolved, err := ResolveRenderPlan(RenderRequest{InputPaths: []string{"one.mp4"}, TransitionEvery: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Transitions) != 0 {
		t.Fatalf("single input must not auto-select transitions: %+v", resolved.Transitions)
	}
	if !resolved.NoTransitions {
		t.Fatal("single input must resolve to NoTransitions=true")
	}
}

// TestResolveRenderPlanDefaultComposeIsPlanExpressible pins the stock compose
// step's DEFAULT request (no operator no-effects/no-transitions override,
// TransitionEvery=1, the canonical effects directory + effect interval) as
// plan-expressible: one cut clip per call has no internal boundary, so the
// resolver must produce zero transitions and zero effect paths. This is the
// exact shape that previously failed with
// "render_stock requires a canonical render_plan" / "unresolved render plan".
func TestResolveRenderPlanDefaultComposeIsPlanExpressible(t *testing.T) {
	canonical := DefaultPipelineConfig()
	resolved, err := ResolveRenderPlan(RenderRequest{
		InputPaths:      []string{"cut-0.mp4"},
		OutputPath:      "/tmp/stock_composed_0.mp4",
		TransitionEvery: 1,
		EffectsDir:      canonical.EffectsDir,
		EffectEvery:     canonical.EffectInterval,
		EffectIndexHint: 0,
	})
	if err != nil {
		t.Fatalf("default compose request must resolve without error: %v", err)
	}
	if len(resolved.Transitions) != 0 || len(resolved.EffectPaths) != 0 {
		t.Fatalf("default compose request must resolve to zero transitions/effects: %+v", resolved)
	}
	if !resolved.NoTransitions || !resolved.NoEffects {
		t.Fatalf("default compose request must resolve to NoTransitions+NoEffects: %+v", resolved)
	}
}

func TestResolveRenderPlanRejectsUnreadableEffectDirectory(t *testing.T) {
	_, err := ResolveRenderPlan(RenderRequest{InputPaths: []string{"one.mp4"}, EffectsDir: filepath.Join(t.TempDir(), "missing"), EffectEvery: 1})
	if err == nil {
		t.Fatal("expected unreadable effect directory to fail closed")
	}
}

func TestResolveRenderPlanRejectsInvalidExplicitAssignments(t *testing.T) {
	_, err := ResolveRenderPlan(RenderRequest{
		InputPaths:  []string{"one.mp4"},
		Transitions: []RenderTransition{{ClipIndex: 1, Segment: "end", ID: "fadeblack"}},
	})
	if err == nil {
		t.Fatal("expected out-of-range transition assignment to fail")
	}
	_, err = ResolveRenderPlan(RenderRequest{
		InputPaths:  []string{"one.mp4"},
		EffectPaths: []RenderEffectPath{{ClipIndex: 0, Path: ""}},
	})
	if err == nil {
		t.Fatal("expected empty effect path assignment to fail")
	}
}

func TestResolveRenderPlanRequiresEffectsWhenTargetsExist(t *testing.T) {
	_, err := ResolveRenderPlan(RenderRequest{
		InputPaths:  []string{"one.mp4"},
		EffectEvery: 1,
	})
	if err == nil {
		t.Fatal("expected missing effects directory to fail closed")
	}
}

func TestResolveRenderPlanPreservesExplicitAssignments(t *testing.T) {
	resolved, err := ResolveRenderPlan(RenderRequest{
		InputPaths:      []string{"one.mp4"},
		Transitions:     []RenderTransition{{ClipIndex: 0, Segment: "end", ID: "fadeblack"}},
		EffectPaths:     []RenderEffectPath{{ClipIndex: 0, Path: "/exact/effect.mp4"}},
		TransitionEvery: 99, EffectEvery: 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Transitions) != 1 || resolved.Transitions[0].ID != "fadeblack" || len(resolved.EffectPaths) != 1 {
		t.Fatalf("explicit assignments changed: %+v %+v", resolved.Transitions, resolved.EffectPaths)
	}
}

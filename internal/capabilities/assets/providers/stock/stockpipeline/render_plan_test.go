package assets

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

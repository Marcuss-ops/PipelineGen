package overlay

import "testing"

func TestAnimationDirectiveExactFailClosed(t *testing.T) {
	d := AnimationDirective{Scope: "scene", SceneRef: "scene_006", Target: TargetMapCamera, Mode: DirectiveExact}
	if err := d.Validate(); err == nil {
		t.Fatalf("exact without motion must fail")
	}
	d.MotionID = "signature_dive"
	if err := d.Validate(); err != nil {
		t.Fatalf("exact with motion ok: %v", err)
	}
	got := NormalizeLegacyFields("scene_006", "signature_dive", "", "route_line")
	if len(got) != 2 {
		t.Fatalf("legacy map fields project to 2 directives, got %d", len(got))
	}
}

package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"testing"
)

// A date card capped by the planner must render exactly that window. The
// generic +2s overlay tail would turn a 3.19s capped card into a 5s clip and
// overlap the next date in the composited timeline.
func TestSeparateDateCardPlanHonoursSceneCappedWindow(t *testing.T) {
	const startUS int64 = 1_607_000
	const durationUS int64 = 3_190_000
	plan := manifestTestPlan()
	plan.Items = []capmodel.OverlayItem{{
		ID: "date-01", SceneID: "scene-01", Kind: "number", TemplateID: "TIMELINE_DATE_CARD",
		PresetID: "phrase_default", MotionID: "date_fade_rise",
		StartMs: startUS / 1000, EndMs: (startUS + durationUS) / 1000,
		StartUS: startUS, DurationUS: durationUS,
	}}
	candidates := overlayRenderableCandidates(plan)
	if len(candidates) != 1 || candidates[0].source.ID != "date-01" {
		t.Fatalf("date candidates = %+v, want one date-01", candidates)
	}
	child, metadata, err := separateOverlayItemPlan(plan, candidates[0].source, candidates[0].index)
	if err != nil {
		t.Fatalf("date child plan: %v", err)
	}
	if len(child.Items) != 1 || child.Items[0].DurationUS != durationUS {
		t.Fatalf("date child duration = %+v, want exactly %dus", child.Items, durationUS)
	}
	if child.DurationMS != durationUS/1000 {
		t.Fatalf("date child plan duration = %dms, want %dms", child.DurationMS, durationUS/1000)
	}
	if metadata == nil || metadata.TargetDurationUS != durationUS {
		t.Fatalf("date render target = %+v, want %dus", metadata, durationUS)
	}
}

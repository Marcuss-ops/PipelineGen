package finaljob

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestApplyFinalJobOverlayWindowPolicyKeepsClipScenePhraseWindow(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "phrase", SceneID: "scene-clip", Kind: "text_phrase",
		StartUS: 12_000_000, DurationUS: 2_000_000,
		StartMs: 12_000, EndMs: 14_000,
	}}}
	if err := applyFinalJobOverlayWindowPolicy(plan, nil); err != nil {
		t.Fatalf("apply clip-scene overlay policy: %v", err)
	}
	item := plan.Items[0]
	if item.StartUS != 12_000_000 || item.DurationUS != 2_000_000 || item.StartMs != 12_000 || item.EndMs != 14_000 {
		t.Fatalf("phrase window was moved away from its scene timing: %+v", item)
	}
}

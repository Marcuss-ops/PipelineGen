package scriptgeneration

import (
	"context"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// fakeDynamicMapGPUGate is the shared RenderingGen GPU admission double used by
// the root e2e map tests. The production gate lives in the renderqueue phase.
type fakeDynamicMapGPUGate struct{ acquired int }

func (g *fakeDynamicMapGPUGate) Acquire(context.Context) (func(), error) {
	g.acquired++
	return func() {}, nil
}

// manifestTestPlan is the small per-item fixture shared by the root render-queue
// boundary tests. The phase package keeps its own copy for its tests.
func manifestTestPlan() capmodel.OverlayPlan {
	return capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        "test-per-item-overlay",
		VideoID:       "test-per-item-overlay",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    100000,
		Items: []capmodel.OverlayItem{
			{
				ID: "item-01", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 10_000_000, DurationUS: 2_000_000,
			},
			{
				ID: "item-02", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 30_000_000, DurationUS: 2_000_000,
			},
			{
				ID: "item-03", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				StartUS: 50_000_000, DurationUS: 2_000_000,
			},
		},
	}
}

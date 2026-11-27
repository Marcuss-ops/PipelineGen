package overlayplan

import (
	"context"
	"encoding/json"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

type capturingCompiler struct {
	request json.RawMessage
	plan    *capmodel.OverlayPlan
}

func (c *capturingCompiler) CompileOverlayPlanV2(_ context.Context, request json.RawMessage) (*capmodel.OverlayPlan, error) {
	c.request = append(json.RawMessage(nil), request...)
	return c.plan, nil
}

// TestCompileOverlayPlanV2StampsTheCreativeSeed is the P0 acceptance test for
// ANIMRAND-01: the run's motion seed must be (a) on the v2 request and (b) on
// the adopted plan as entity_variation_seed, so RenderingGen resamples a new
// render instead of freezing on the project identity.
func TestCompileOverlayPlanV2StampsTheCreativeSeed(t *testing.T) {
	compiler := &capturingCompiler{plan: &capmodel.OverlayPlan{PlanID: "plan-1", VideoID: "video-1"}}
	plan, err := CompileOverlayPlanV2(
		context.Background(),
		compiler,
		&contract.GenerateResult{},
		contract.Language("it"),
		OverlayCanvasSpec{},
		"plan-1", "video-1", "project-1", "creative-seed-7",
		nil, nil, nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("CompileOverlayPlanV2: %v", err)
	}
	if plan.EntityVariationSeed != "creative-seed-7" {
		t.Fatalf("entity_variation_seed=%q, want the run creative seed", plan.EntityVariationSeed)
	}
	var request overlayplan.V2Request
	if err := json.Unmarshal(compiler.request, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Plan.MotionSeed != "creative-seed-7" {
		t.Fatalf("request motion_seed=%q, want the run creative seed", request.Plan.MotionSeed)
	}
}

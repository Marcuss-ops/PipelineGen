package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func TestPendingOverlayPlansRequireObjectStoreForFinalJobHandoff(t *testing.T) {
	plan := &capmodel.OverlayPlan{PlanID: "plan-1", Language: "pt", Items: []capmodel.OverlayItem{{ID: "item-1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE"}}}
	result := &GenerateResult{OverlayPlan: plan}

	pendingOverlayPlans(result, GenerateRequest{FinalJob: true})
	if !plan.RequireObjectStoreBeforeReturn {
		t.Fatal("final_job overlay plan must require its certified Objectstore artifact before PREPARE")
	}

	plan.RequireObjectStoreBeforeReturn = false
	pendingOverlayPlans(result, GenerateRequest{})
	if plan.RequireObjectStoreBeforeReturn {
		t.Fatal("ordinary overlay plan must not require final-job Objectstore delivery")
	}
}

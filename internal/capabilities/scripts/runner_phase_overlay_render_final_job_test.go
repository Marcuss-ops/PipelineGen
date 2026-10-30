package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestPendingOverlayPlansRequireObjectStoreForFinalJobHandoff(t *testing.T) {
	plan := &capabilityoverlay.OverlayPlan{PlanID: "plan-1", Language: "pt"}
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

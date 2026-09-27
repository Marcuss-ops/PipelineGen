package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestPendingOverlayPlansRequireDriveBeforeFinalJobHandoff(t *testing.T) {
	plan := &capabilityoverlay.OverlayPlan{PlanID: "plan-1", Language: "pt"}
	result := &GenerateResult{OverlayPlan: plan}

	pendingOverlayPlans(result, GenerateRequest{FinalJob: true})
	if !plan.RequireDriveBeforeReturn {
		t.Fatal("final_job overlay plan must require its published Drive identity before PREPARE")
	}

	plan.RequireDriveBeforeReturn = false
	pendingOverlayPlans(result, GenerateRequest{})
	if plan.RequireDriveBeforeReturn {
		t.Fatal("ordinary overlay plan must keep asynchronous Drive publication")
	}
}

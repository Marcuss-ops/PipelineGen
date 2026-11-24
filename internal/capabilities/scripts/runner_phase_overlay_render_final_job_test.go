package scriptgeneration

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// Every overlay render is an intermediate, final-job or not: the local join
// and the remote handoff both fetch the certified object-store locator. A plan
// that could still be published to Drive would copy bytes nobody reads from
// there.
func TestPendingOverlayPlansKeepEveryOverlayInTheObjectStore(t *testing.T) {
	plan := &capmodel.OverlayPlan{PlanID: "plan-1", Language: "pt", Items: []capmodel.OverlayItem{{ID: "item-1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE"}}}
	result := &contract.GenerateResult{OverlayPlan: plan}

	pendingOverlayPlans(result, contract.GenerateRequest{FinalJob: true})
	if !plan.RequireObjectStoreBeforeReturn {
		t.Fatal("final_job overlay plan must require its certified Objectstore artifact before PREPARE")
	}

	plan.RequireObjectStoreBeforeReturn = false
	pendingOverlayPlans(result, contract.GenerateRequest{})
	if !plan.RequireObjectStoreBeforeReturn {
		t.Fatal("ordinary overlay plan is an intermediate too: it must stay in Objectstore, not be published to Drive")
	}
}

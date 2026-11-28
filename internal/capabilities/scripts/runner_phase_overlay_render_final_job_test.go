package scriptgeneration

import (
	"strings"
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

func TestClipImageBackgroundCoverageRequiresEveryImageScene(t *testing.T) {
	plans := []languageOverlayPlan{{plan: &capmodel.OverlayPlan{
		SceneBackgrounds: map[string]*capmodel.OverlayBackground{
			"scene-01": {Kind: "video", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "clip-1", LocalPath: "/clips/one.mp4", SHA256: "sha-1"}}},
		},
		Items: []capmodel.OverlayItem{
			{ID: "image-01", SceneID: "scene-01", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "image-1", SHA256: "image-sha"}}},
			{ID: "image-02", SceneID: "scene-02", Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "image-2", SHA256: "image-sha-2"}}},
		},
	}}}
	err := validateClipImageBackgroundCoverage(plans)
	if err == nil || !strings.Contains(err.Error(), `image overlay "image-02"`) || !strings.Contains(err.Error(), `scene "scene-02"`) {
		t.Fatalf("coverage error = %v, want missing scene-02 source background", err)
	}
	plans[0].plan.SceneBackgrounds["scene-02"] = &capmodel.OverlayBackground{Kind: "video", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "clip-2", LocalPath: "/clips/two.mp4", SHA256: "sha-2"}}}
	if err := validateClipImageBackgroundCoverage(plans); err != nil {
		t.Fatalf("complete scene background coverage: %v", err)
	}
}

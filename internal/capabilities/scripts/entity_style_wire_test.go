package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestEntityStyleIdRidesTheOverlayPlanWire(t *testing.T) {
	item := capmodel.OverlayItem{
		ID: "ada-card", Kind: string(capmodel.KindEntityImage),
		TemplateID: "image_popup", EntityCaption: "Ada Lovelace",
		AssetRefs:     []capmodel.OverlayAssetRef{{AssetID: "ada", SHA256: "ada-sha"}},
		EntityStyleID: capabilityoverlay.IdentityEntityStyleSelector,
	}
	wire, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"entity_style_id":"random"`) {
		t.Fatalf("entity_style_id missing from the overlay-plan wire: %s", wire)
	}
	// The renderer contract field must round-trip through the queue wire
	// conversion, which strips producer-only keys but keeps contract fields.
	wirePlan, err := marshalRenderingGenOverlayPlan(capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "style-wire", VideoID: "video",
		Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Items: []capmodel.OverlayItem{item},
	})
	if err != nil {
		t.Fatalf("marshal semantic chronon plan: %v", err)
	}
	if !strings.Contains(string(wirePlan), `"entity_style_id":"random"`) {
		t.Fatalf("entity_style_id dropped by the queue wire conversion: %s", wirePlan)
	}
}

func TestEntityStyleIdChangesTheRenderKey(t *testing.T) {
	plan := capmodel.OverlayPlan{PlanID: "style-key", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1}
	item := capmodel.OverlayItem{
		ID: "ada-card", Kind: string(capmodel.KindEntityImage), TemplateID: "image_popup",
		EntityCaption: "Ada", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "ada"}},
	}
	keyWithout := capmodel.ComputeRenderKey(plan, item)
	item.EntityStyleID = capabilityoverlay.IdentityEntityStyleSelector
	keyWith := capmodel.ComputeRenderKey(plan, item)
	if keyWithout == keyWith {
		t.Fatal("entity_style_id must participate in the render key")
	}
}

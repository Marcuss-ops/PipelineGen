package scriptgeneration

import (
	"encoding/json"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
	"testing"

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

func TestAssignEntityImageMotionsStampsTheStyleSelector(t *testing.T) {
	items := []capmodel.OverlayItem{
		{ID: "portrait", Kind: string(capmodel.KindEntityImage), EntityCaption: "Ada", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "ada"}}},
		{ID: "silent", Kind: string(capmodel.KindEntityImage), AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "b"}}},
	}
	if err := assignEntityImageMotions(items, 0, 1920, 1080, ""); err != nil {
		t.Fatalf("assign entity image motions: %v", err)
	}
	if items[0].EntityStyleID != "" {
		t.Fatalf("default portrait style = %q, want no implicit composition selector", items[0].EntityStyleID)
	}
	if items[1].EntityStyleID != "" {
		t.Fatal("caption-less entity image must not carry entity_style_id (renderer precondition)")
	}
}

// TestAssignEntityImageMotionsKeepsAPinnedSelector pins the channel-profile
// contract end to end at the final stamp site: a run-level pin (badge, camera,
// …) survives the motion-assignment pass instead of being reset to "random".
func TestAssignEntityImageMotionsKeepsAPinnedSelector(t *testing.T) {
	for _, pinned := range []string{"badge", "camera", "typewriter"} {
		items := []capmodel.OverlayItem{
			{ID: "portrait", Kind: string(capmodel.KindEntityImage), EntityCaption: "Ada", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "ada"}}},
		}
		if err := assignEntityImageMotions(items, 0, 1920, 1080, pinned); err != nil {
			t.Fatalf("assign entity image motions (%s): %v", pinned, err)
		}
		if items[0].EntityStyleID != pinned {
			t.Fatalf("pinned selector %q was clobbered to %q by the final stamp", pinned, items[0].EntityStyleID)
		}
	}
}

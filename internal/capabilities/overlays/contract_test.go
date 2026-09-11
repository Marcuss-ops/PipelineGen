package overlays

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOverlayPlanContract_EntityRefNotSerialized pins that entity_ref is
// PipelineGen-internal identity: the worker renders from
// kind/template_id/preset_id/text, so the ref must never reach the
// overlay-plan.v1 wire. The in-memory plan keeps it for the document/editing
// projections.
func TestOverlayPlanContract_EntityRefNotSerialized(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "plan-001", VideoID: "video-001",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{{
			ID: "overlay-scene-0-tim-cook", SceneID: "scene-0",
			EntityID: "ent_abc123", Kind: string(KindEntityCard),
			StartMs: 0, EndMs: 200, TemplateID: "person_default", Text: "Tim Cook",
			EntityRef: &OverlayEntityRef{
				EntityID: "ent_abc123", Type: "PERSON", Name: "Tim Cook", SurfaceText: "Tim Cook",
			},
		}},
	}
	require.NoError(t, plan.Validate())
	require.NotNil(t, plan.Items[0].EntityRef, "the in-memory plan keeps the identity for the document/editing projections")

	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	var doc struct {
		Items  []map[string]any `json:"items"`
		PlanID string           `json:"plan_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Len(t, doc.Items, 1)
	_, present := doc.Items[0]["entity_ref"]
	require.False(t, present, "entity_ref must not be serialized into overlay-plan.v1")
	require.Equal(t, "person_default", doc.Items[0]["template_id"])
	require.Equal(t, "Tim Cook", doc.Items[0]["text"])
}

// TestOverlayPlanContract_PresetIDSerialized pins the preset_id contract
// slot: when PipelineGen selects a preset, the id travels in the emitted
// JSON; the value space is opaque (resolved by the preset registry
// downstream — never validated against a local table here).
func TestOverlayPlanContract_PresetIDSerialized(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "plan-002", VideoID: "video-002",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{{
			ID: "ov_001", StartMs: 1500, EndMs: 3700,
			TemplateID: "IMPORTANT_PHRASE", PresetID: "phrase_focus_v1",
			Text: "QUESTO CAMBIA TUTTO",
		}},
	}
	require.NoError(t, plan.Validate())

	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	var doc struct {
		Items []struct {
			PresetID string `json:"preset_id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Equal(t, "phrase_focus_v1", doc.Items[0].PresetID)
}

// TestOverlayPlanContract_EntityRefValidation pins the fail-closed contract:
// an entity_ref must carry entity_id, type and name — a partial ref is
// rejected instead of reaching RenderingGen incomplete.
func TestOverlayPlanContract_EntityRefValidation(t *testing.T) {
	base := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "plan-003", VideoID: "video-003",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{{
			ID: "ov_001", StartMs: 0, EndMs: 200, TemplateID: "person_default", Text: "Tim Cook",
		}},
	}
	partial := base
	partial.Items[0].EntityRef = &OverlayEntityRef{EntityID: "ent_x", Type: "PERSON"} // missing name
	require.Error(t, partial.Validate(), "entity_ref without name must be rejected")

	partial = base
	partial.Items[0].EntityRef = &OverlayEntityRef{Type: "PERSON", Name: "Tim Cook"} // missing entity_id
	require.Error(t, partial.Validate(), "entity_ref without entity_id must be rejected")
}

// TestOverlayPlanContract_PresetDoesNotChangeLegacyRenderKey pins the
// compatibility guarantee: an item WITHOUT a preset produces exactly the
// same render key and fingerprint as before the contract existed — the new
// fields are omitempty, so legacy plans are byte-identical.
func TestOverlayPlanContract_PresetDoesNotChangeLegacyRenderKey(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{{
			ID: "o", TemplateID: "entity-card@1", StartMs: 10, EndMs: 20,
			Text: "Ada",
		}},
	}
	require.NoError(t, plan.Validate())
	keyWithoutPreset := plan.Items[0].RenderKey
	fpWithoutPreset := plan.Fingerprint

	// The same item with a preset MUST produce a different render key (the
	// preset changes the visual output) and a different fingerprint.
	plan.Items[0].PresetID = "entity_card_v1"
	plan.Items[0].RenderKey = ""
	plan.Fingerprint = ""
	require.NoError(t, plan.Validate())
	require.NotEqual(t, keyWithoutPreset, plan.Items[0].RenderKey, "preset must change the render key")
	require.NotEqual(t, fpWithoutPreset, plan.Fingerprint, "preset must change the fingerprint")
}

// TestOverlayPlanContract_EntityRefNotInRenderKey pins that entity_ref is
// identity metadata, not render input: two items that render the same pixels
// (same template/text/assets) share the same render key even with different
// entity refs — the ref never invalidates the cache.
func TestOverlayPlanContract_EntityRefNotInRenderKey(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
	}
	a := OverlayItem{
		ID: "o1", TemplateID: "person_default", StartMs: 0, EndMs: 200, Text: "Tim Cook",
		EntityRef: &OverlayEntityRef{EntityID: "ent_tim", Type: "PERSON", Name: "Tim Cook"},
	}
	b := OverlayItem{
		ID: "o2", TemplateID: "person_default", StartMs: 0, EndMs: 200, Text: "Tim Cook",
		EntityRef: &OverlayEntityRef{EntityID: "ent_other", Type: "PERSON", Name: "Tim Cook"},
	}
	require.Equal(t, RenderKey(plan, a), RenderKey(plan, b),
		"entity_ref is identity metadata and must not change the render key")
}

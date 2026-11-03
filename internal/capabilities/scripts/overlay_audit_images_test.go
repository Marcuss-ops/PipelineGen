package scriptgeneration

import (
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/stretchr/testify/require"
)

// auditPersonID is the stable identity the plan and the resolver both join on:
// the audit must work from THAT id, not from a spelling.
func auditPersonID(name string) string {
	return capabilityentities.StableEntityID("PERSON", name)
}

// auditTestEntity is one detected entity of a plan.
func auditTestEntity(name string) capabilityoverlay.ResolvedEntity {
	return capabilityoverlay.ResolvedEntity{
		EntityID: auditPersonID(name), Type: "PERSON", Text: name, CanonicalText: name,
		SceneID: "scene-1", Confidence: 0.9,
	}
}

func auditTestEntityCard(id, sceneID, name string) capabilityoverlay.OverlayItem {
	return capabilityoverlay.OverlayItem{
		ID: id, SceneID: sceneID, Kind: "entity_card", TemplateID: "person_default",
		EntityID: auditPersonID(name), StartMs: 1000, EndMs: 4000,
		EntityRef: &capabilityoverlay.OverlayEntityRef{
			EntityID: auditPersonID(name), Type: "PERSON", Name: name, SurfaceText: name,
		},
	}
}

func auditTestImageItem(id, sceneID, name string) capabilityoverlay.OverlayItem {
	item := auditTestEntityCard(id, sceneID, name)
	item.Kind = "entity_image"
	item.TemplateID = "image_popup"
	item.AssetRefs = []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-" + auditPersonID(name)}}
	return item
}

// TestOverlayAuditEntityImagesExplainsEveryMissingImage pins the question the
// r20minuti artifact could not answer: the image budget was 3, no image was
// planned, and nothing said whether the resolver produced no verified asset or
// the planner failed to bind one it had. Those are different owners and
// different fixes, so they are different reasons.
func TestOverlayAuditEntityImagesExplainsEveryMissingImage(t *testing.T) {
	plan := &capabilityoverlay.OverlayPlan{
		PlanID: "plan-1", VideoID: "video-1",
		Intake: &capabilityoverlay.OverlayPlanIntake{
			Budget: capabilityoverlay.OverlayIntakeBudget{ImageOverlays: 3},
		},
		Items: []capabilityoverlay.OverlayItem{
			auditTestImageItem("ov-1", "scene-1", "Enzo Boschetti"),
			// Text cards are not image promises; only image-bearing plan items
			// participate in the image coverage gate.
			auditTestEntityCard("ov-2", "scene-1", "Maria Rossi"),
			auditTestEntityCard("ov-3", "scene-1", "Carlo Bianchi"),
		},
	}
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{
			auditTestEntity("Enzo Boschetti"),
			auditTestEntity("Maria Rossi"),
			auditTestEntity("Carlo Bianchi"),
			// Detected but never planned for the video: it must not inflate the
			// missing list, or every mention of every name would read as a bug.
			auditTestEntity("Davide Ferrero"),
		},
		Assets: []capabilityoverlay.BoundAsset{
			{EntityID: auditPersonID("Enzo Boschetti"), AssetID: "asset-" + auditPersonID("Enzo Boschetti"), Verified: true},
			// Verified bytes the planner never bound: a planner defect, not a
			// media miss, and the audit must say so.
			{EntityID: auditPersonID("Maria Rossi"), AssetID: "asset-" + auditPersonID("Maria Rossi"), Verified: true},
		},
	}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	require.NotNil(t, audit)
	require.NotNil(t, audit.EntityImages)
	images := audit.EntityImages
	require.False(t, images.Required, "the strict gate is opt-in, the audit is always emitted")
	require.Equal(t, 3, images.Budget)
	require.Equal(t, 1, images.BudgetUsed)
	require.Equal(t, 2, images.BudgetUnused)
	require.Equal(t, 1, images.Selected)
	require.Equal(t, 1, images.Bound)
	require.Equal(t, 0, images.Missing)
	require.Equal(t, 1, images.Reasons[EntityImageReasonBound])
	require.Empty(t, images.MissingEntities)

	// The gate is opt-in and evaluates only image-bearing plan items.
	require.NoError(t, enforceEntityImageGate(audit))
	t.Setenv(requireEntityImagesEnv, "1")
	strict := buildOverlayAuditWithLineage(bundle, plan, nil)
	require.NotNil(t, strict.EntityImages)
	err := enforceEntityImageGate(strict)
	require.NoError(t, err)
}

// TestFreezeOverlayIntentsReportsWhyAnIntentDidNotFreeze pins the lineage: an
// authored intent that never became a timed item is a PROPOSAL, and the reason
// distinguishes "the entity was never planned" from "the planner rewrote the
// text". Before this, 31 of 39 intents stayed PENDING with no explanation.
func TestFreezeOverlayIntentsReportsWhyAnIntentDidNotFreeze(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		auditTestImageItem("ov-1", "scene-1", "Enzo Boschetti"),
		{ID: "ov-2", SceneID: "scene-2", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "un testo riscritto", StartMs: 5000, EndMs: 7000},
	}
	intents := []capabilityoverlay.OverlayIntent{
		{
			IntentID: "intent-frozen", SceneID: "scene-1", Source: capabilityoverlay.IntentSourceEntity,
			Entity: capabilityoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Enzo Boschetti"},
			Kind:   "entity_card", TemplateID: "person_default", TimingState: capabilityoverlay.TimingStatePending,
		},
		{
			IntentID: "intent-entity-missing", SceneID: "scene-3", Source: capabilityoverlay.IntentSourceEntity,
			Entity: capabilityoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Maria Rossi"},
			Kind:   "entity_card", TemplateID: "person_default", TimingState: capabilityoverlay.TimingStatePending,
		},
		{
			IntentID: "intent-text-rewritten", SceneID: "scene-2", Source: capabilityoverlay.IntentSourceImportantPhrase,
			SourceText: "testo originale", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			TimingState: capabilityoverlay.TimingStatePending,
		},
		{
			IntentID: "intent-text-kept", SceneID: "scene-2", Source: capabilityoverlay.IntentSourceImportantPhrase,
			SourceText: "un testo riscritto", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			TimingState: capabilityoverlay.TimingStatePending,
		},
	}

	lineage := freezeOverlayIntents(intents, items)
	require.NotNil(t, lineage)
	require.Equal(t, 4, lineage.Authored)
	require.Equal(t, 2, lineage.Frozen)
	require.Equal(t, 2, lineage.Unresolved)
	require.Equal(t, lineage.Authored, lineage.Frozen+lineage.Unresolved,
		"every authored intent is either rendered or explained")
	require.Equal(t, 2, lineage.BySource[string(capabilityoverlay.IntentSourceEntity)])
	require.Equal(t, 1, lineage.Reasons[OverlayIntentReasonEntityNotPlanned])
	require.Equal(t, 1, lineage.Reasons[OverlayIntentReasonSourceTextNotInPlan])
	require.Len(t, lineage.Items, 2)
	reasons := map[string]string{}
	for _, item := range lineage.Items {
		reasons[item.IntentID] = item.Reason
	}
	require.Equal(t, OverlayIntentReasonEntityNotPlanned, reasons["intent-entity-missing"])
	require.Equal(t, OverlayIntentReasonSourceTextNotInPlan, reasons["intent-text-rewritten"])

	// The frozen intents carry the plan's certified facts, so the persisted
	// surface cannot disagree with what RenderingGen renders.
	require.Equal(t, capabilityoverlay.TimingStateFrozen, intents[0].TimingState)
	require.Equal(t, "entity_image", intents[0].Kind)
	require.Equal(t, int64(1000), intents[0].StartMs)

	// The audit projects the lineage instead of re-deriving it.
	audit := buildOverlayAuditWithLineage(&capabilityoverlay.SemanticRenderBundleV1{}, &capabilityoverlay.OverlayPlan{Items: items}, lineage)
	require.NotNil(t, audit.OverlayTiming)
	require.Equal(t, 4, audit.OverlayTiming.Authored)
	require.Equal(t, 2, audit.OverlayTiming.Frozen)
	require.Equal(t, 2, audit.OverlayTiming.Unresolved)
	require.Len(t, audit.OverlayTiming.UnresolvedItems, 2)
}

// TestOverlayAuditEntityImagesCountsOnlyImagePlans keeps map/location labels
// outside the image-coverage gate, while an explicit entity-image overlay for
// that place remains covered.
func TestOverlayAuditEntityImagesCountsOnlyImagePlans(t *testing.T) {
	for _, spelling := range []string{"GPE", "LOCATION", "CITY", "COUNTRY", "PLACE"} {
		t.Run(spelling, func(t *testing.T) {
			placeID := capabilityentities.StableEntityID(spelling, "Garlasco")
			plan := &capabilityoverlay.OverlayPlan{
				PlanID: "plan-place",
				Intake: &capabilityoverlay.OverlayPlanIntake{
					Budget: capabilityoverlay.OverlayIntakeBudget{ImageOverlays: 2},
				},
				Items: []capabilityoverlay.OverlayItem{{
					ID: "ov-place", SceneID: "scene-1", Kind: "location", TemplateID: "LOCATION",
					EntityID: placeID, StartMs: 1000, EndMs: 4000,
					EntityRef: &capabilityoverlay.OverlayEntityRef{
						EntityID: placeID, CanonicalEntityID: strings.ToLower(spelling) + ":garlasco",
						Type: spelling, Name: "Garlasco", SurfaceText: "Garlasco",
					},
				}},
			}
			bundle := &capabilityoverlay.SemanticRenderBundleV1{
				Entities: []capabilityoverlay.ResolvedEntity{{
					EntityID: placeID, Type: spelling, Text: "Garlasco", CanonicalText: "Garlasco",
					SceneID: "scene-1", Confidence: 0.9,
				}},
			}

			audit := buildOverlayAuditWithLineage(bundle, plan, nil)
			require.NotNil(t, audit.EntityImages)
			require.True(t, audit.DetectedEntities[0].SelectedForVideo)
			require.Equal(t, 0, audit.EntityImages.Selected,
				"a location label is not an image promise")
			require.Equal(t, 0, audit.EntityImages.Missing)

			plan.Items = append(plan.Items, capabilityoverlay.OverlayItem{
				ID: "ov-place-image", SceneID: "scene-1", Kind: "entity_image", TemplateID: "image_popup",
				EntityID: placeID, EntityRef: plan.Items[0].EntityRef,
			})
			audit = buildOverlayAuditWithLineage(bundle, plan, nil)
			require.Equal(t, 1, audit.EntityImages.Selected)
			require.Equal(t, 1, audit.EntityImages.Missing)

			// A verified asset bound by the plan flips the same expectation to
			// "bound" — the place is counted, not merely flagged.
			plan.Items[0].AssetRefs = []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-garlasco"}}
			bundle.Assets = []capabilityoverlay.BoundAsset{{EntityID: placeID, AssetID: "asset-garlasco", Verified: true}}
			audit = buildOverlayAuditWithLineage(bundle, plan, nil)
			require.Equal(t, 1, audit.EntityImages.Selected)
			require.Equal(t, 1, audit.EntityImages.Bound)
			require.Equal(t, 0, audit.EntityImages.Missing)
		})
	}
}

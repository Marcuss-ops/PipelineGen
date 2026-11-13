package scriptgeneration

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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

func auditTestEntityCard(id, sceneID, name string) capmodel.OverlayItem {
	return capmodel.OverlayItem{
		ID: id, SceneID: sceneID, Kind: "entity_card", TemplateID: "person_default",
		EntityID: auditPersonID(name), StartMs: 1000, EndMs: 4000,
		EntityRef: &capmodel.OverlayEntityRef{
			EntityID: auditPersonID(name), Type: "PERSON", Name: name, SurfaceText: name,
		},
	}
}

func auditTestImageItem(id, sceneID, name string) capmodel.OverlayItem {
	item := auditTestEntityCard(id, sceneID, name)
	item.Kind = "entity_image"
	item.TemplateID = "image_popup"
	item.AssetRefs = []capmodel.OverlayAssetRef{{AssetID: "asset-" + auditPersonID(name)}}
	return item
}

// TestOverlayAuditEntityImagesExplainsEveryMissingImage pins the question the
// r20minuti artifact could not answer: the image budget was 3, no image was
// planned, and nothing said whether the resolver produced no verified asset or
// the planner failed to bind one it had. Those are different owners and
// different fixes, so they are different reasons.
func TestOverlayAuditEntityImagesExplainsEveryMissingImage(t *testing.T) {
	plan := &capmodel.OverlayPlan{
		PlanID: "plan-1", VideoID: "video-1",
		Intake: &capmodel.OverlayPlanIntake{
			Budget: capmodel.OverlayIntakeBudget{ImageOverlays: 3},
		},
		Items: []capmodel.OverlayItem{
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

func TestGateCountsPlannedEntityImageCardsOnly(t *testing.T) {
	t.Setenv(requireEntityImagesEnv, "")
	bundle := &capabilityoverlay.SemanticRenderBundleV1{Entities: []capabilityoverlay.ResolvedEntity{
		auditTestEntity("Ana Rossi"), auditTestEntity("Marco Bianchi"),
	}}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{auditTestEntityCard("card-1", "scene-1", "Ana Rossi")}}
	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	applyRequestedEntityImageContract(audit, bundle, plan, true, false, []string{"PERSON"})
	if audit.EntityImages == nil || audit.EntityImages.Required || audit.EntityImages.Selected != 0 || audit.EntityImages.Missing != 0 {
		t.Fatalf("requested person-image audit = %+v; a text fallback is not an image promise", audit.EntityImages)
	}
	if len(audit.EntityImages.Reasons) != 0 {
		t.Fatalf("text fallback reasons = %+v, want no image promise", audit.EntityImages.Reasons)
	}
	if audit.DetectedEntities[0].ImageRequired || audit.DetectedEntities[1].ImageRequired {
		t.Fatal("neither a text fallback nor an unplanned mention may inflate the image denominator")
	}

	t.Setenv(requireEntityImagesEnv, "1")
	strict := buildOverlayAuditWithLineage(bundle, plan, nil)
	applyRequestedEntityImageContract(strict, bundle, plan, true, false, []string{"PERSON"})
	require.True(t, strict.EntityImages.Required)
	require.NoError(t, enforceEntityImageGate(strict), "a planned text fallback must not fail the strict gate")
}

func TestEntityImageGateDeduplicatesRepeatedIdentityOccurrences(t *testing.T) {
	t.Setenv(requireEntityImagesEnv, "")
	names := []string{"Francis Kaufmann", "Anastasia Trofimova", "Villa Pamphili"}
	bundle := &capabilityoverlay.SemanticRenderBundleV1{}
	plan := &capmodel.OverlayPlan{}
	for _, name := range names {
		stable := auditPersonID(name)
		bundle.Assets = append(bundle.Assets, capabilityoverlay.BoundAsset{
			EntityID: stable, AssetID: "asset-" + stable, ContentHash: strings.Repeat("a", 64),
			SourceURL: "https://drive.google.com/uc?export=download&id=" + stable, Verified: true,
		})
		plan.Items = append(plan.Items, auditTestImageItem("image-"+stable, "scene-1", name))
		for scene := 0; scene < 6; scene++ {
			entity := auditTestEntity(name)
			entity.SceneID = "scene-" + string(rune('a'+scene))
			bundle.Entities = append(bundle.Entities, entity)
		}
	}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	applyRequestedEntityImageContract(audit, bundle, plan, true, false, []string{"PERSON"})
	require.NotNil(t, audit.EntityImages)
	require.Equal(t, 3, audit.EntityImages.Selected, "three identities, not eighteen occurrences")
	require.Equal(t, 3, audit.EntityImages.Bound)
	require.Equal(t, 0, audit.EntityImages.Missing)
	require.False(t, audit.EntityImages.Required, "the strict gate stays opt-in")
	require.NoError(t, enforceEntityImageGate(audit))

	t.Setenv(requireEntityImagesEnv, "1")
	strict := buildOverlayAuditWithLineage(bundle, plan, nil)
	applyRequestedEntityImageContract(strict, bundle, plan, true, false, []string{"PERSON"})
	require.True(t, strict.EntityImages.Required)
	require.NoError(t, enforceEntityImageGate(strict))
}

// TestVerifiedAssetNotPlannedIsNotABindingFailure pins the r41 vocabulary bug:
// a verified portrait that the planner did not select is an editorial choice,
// not a missing binding. It must stay outside the strict gate's denominator.
func TestVerifiedAssetNotPlannedIsNotBindingMissing(t *testing.T) {
	t.Setenv(requireEntityImagesEnv, "1")
	name := "Anastasia Trofimova"
	stable := auditPersonID(name)
	bundle := &capabilityoverlay.SemanticRenderBundleV1{}
	for scene := 0; scene < 18; scene++ {
		entity := auditTestEntity(name)
		entity.SceneID = fmt.Sprintf("scene-%02d", scene)
		bundle.Entities = append(bundle.Entities, entity)
		bundle.Assets = append(bundle.Assets, capabilityoverlay.BoundAsset{
			EntityID: stable, AssetID: fmt.Sprintf("asset-%02d", scene), Verified: true,
		})
	}

	audit := buildOverlayAuditWithLineage(bundle, &capmodel.OverlayPlan{}, nil)
	applyRequestedEntityImageContract(audit, bundle, nil, true, false, []string{"PERSON"})
	require.NotNil(t, audit.EntityImages)
	require.Equal(t, 0, audit.EntityImages.Selected, "unplanned mentions are not image promises")
	require.NoError(t, enforceEntityImageGate(audit), "a verified-but-unplanned asset must not fail the gate")

	outcome := entityImageOutcome(OverlayAuditEntity{
		ID: stable, Type: "PERSON", Text: name, ImageStatus: "verified_asset_not_planned",
	}, map[string]bool{stable: true})
	require.Equal(t, EntityImageReasonNotSelected, outcome)
}

// TestFreezeOverlayIntentsReportsWhyAnIntentDidNotFreeze pins the lineage: an
// authored intent that never became a timed item is a PROPOSAL, and the reason
// distinguishes "the entity was never planned" from "the planner rewrote the
// text". Before this, 31 of 39 intents stayed PENDING with no explanation.
func TestFreezeOverlayIntentsReportsWhyAnIntentDidNotFreeze(t *testing.T) {
	items := []capmodel.OverlayItem{
		auditTestImageItem("ov-1", "scene-1", "Enzo Boschetti"),
		{ID: "ov-2", SceneID: "scene-2", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "un testo riscritto", StartMs: 5000, EndMs: 7000},
	}
	intents := []capmodel.OverlayIntent{
		{
			IntentID: "intent-frozen", SceneID: "scene-1", Source: capmodel.IntentSourceEntity,
			Entity: capmodel.EntityBinding{Type: "PERSON", CanonicalName: "Enzo Boschetti"},
			Kind:   "entity_card", TemplateID: "person_default", TimingState: capmodel.TimingStatePending,
		},
		{
			IntentID: "intent-entity-missing", SceneID: "scene-3", Source: capmodel.IntentSourceEntity,
			Entity: capmodel.EntityBinding{Type: "PERSON", CanonicalName: "Maria Rossi"},
			Kind:   "entity_card", TemplateID: "person_default", TimingState: capmodel.TimingStatePending,
		},
		{
			IntentID: "intent-text-rewritten", SceneID: "scene-2", Source: capmodel.IntentSourceImportantPhrase,
			SourceText: "testo originale", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			TimingState: capmodel.TimingStatePending,
		},
		{
			IntentID: "intent-text-kept", SceneID: "scene-2", Source: capmodel.IntentSourceImportantPhrase,
			SourceText: "un testo riscritto", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			TimingState: capmodel.TimingStatePending,
		},
	}

	lineage := freezeOverlayIntents(intents, items)
	require.NotNil(t, lineage)
	require.Equal(t, 4, lineage.Authored)
	require.Equal(t, 2, lineage.Frozen)
	require.Equal(t, 2, lineage.Unresolved)
	require.Equal(t, lineage.Authored, lineage.Frozen+lineage.Unresolved,
		"every authored intent is either rendered or explained")
	require.Equal(t, 2, lineage.BySource[string(capmodel.IntentSourceEntity)])
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
	require.Equal(t, capmodel.TimingStateFrozen, intents[0].TimingState)
	require.Equal(t, "entity_image", intents[0].Kind)
	require.Equal(t, int64(1000), intents[0].StartMs)

	// The audit projects the lineage instead of re-deriving it.
	audit := buildOverlayAuditWithLineage(&capabilityoverlay.SemanticRenderBundleV1{}, &capmodel.OverlayPlan{Items: items}, lineage)
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
			plan := &capmodel.OverlayPlan{
				PlanID: "plan-place",
				Intake: &capmodel.OverlayPlanIntake{
					Budget: capmodel.OverlayIntakeBudget{ImageOverlays: 2},
				},
				Items: []capmodel.OverlayItem{{
					ID: "ov-place", SceneID: "scene-1", Kind: "location", TemplateID: "LOCATION",
					EntityID: placeID, StartMs: 1000, EndMs: 4000,
					EntityRef: &capmodel.OverlayEntityRef{
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

			plan.Items = append(plan.Items, capmodel.OverlayItem{
				ID: "ov-place-image", SceneID: "scene-1", Kind: "entity_image", TemplateID: "image_popup",
				EntityID: placeID, EntityRef: plan.Items[0].EntityRef,
			})
			audit = buildOverlayAuditWithLineage(bundle, plan, nil)
			require.Equal(t, 1, audit.EntityImages.Selected)
			require.Equal(t, 1, audit.EntityImages.Missing)

			// A verified asset bound by the plan flips the same expectation to
			// "bound" — the place is counted, not merely flagged.
			plan.Items[0].AssetRefs = []capmodel.OverlayAssetRef{{AssetID: "asset-garlasco"}}
			bundle.Assets = []capabilityoverlay.BoundAsset{{EntityID: placeID, AssetID: "asset-garlasco", Verified: true}}
			audit = buildOverlayAuditWithLineage(bundle, plan, nil)
			require.Equal(t, 1, audit.EntityImages.Selected)
			require.Equal(t, 1, audit.EntityImages.Bound)
			require.Equal(t, 0, audit.EntityImages.Missing)
		})
	}
}

// TestRequestedEntityImageContractSkipsUnadmittedVerifiedAssets pins the
// boundary between media acquisition and the gate: an asset can be verified
// while the semantic production plan deliberately did not admit that identity
// for a card. The audit must not turn that policy decision into a missing-image
// failure.
func TestRequestedEntityImageContractSkipsUnadmittedVerifiedAssets(t *testing.T) {
	audit := &OverlayAudit{DetectedEntities: []OverlayAuditEntity{{
		ID: "ent-italia", Type: "PERSON", Text: "Italia", CanonicalText: "Italia",
		SelectedForVideo: true, ImageStatus: "verified_asset_not_planned",
	}}}
	applyRequestedEntityImageContract(audit, nil, nil, true, true, []string{"PERSON"},
		map[string]struct{}{"anastasia trofimova": {}})

	require.NotNil(t, audit.EntityImages)
	require.False(t, audit.DetectedEntities[0].ImageRequired)
	require.Equal(t, 0, audit.EntityImages.Selected)
	require.Equal(t, 0, audit.EntityImages.Missing)
}

// TestRequestedEntityImageContractStillAuditsPlannedUnadmittedImage proves the
// filter only removes identities that never entered the plan. Once an image
// item exists, the Drive contract is audited even if the identity was not
// admitted: the plan already promised those bytes.
func TestRequestedEntityImageContractStillAuditsPlannedUnadmittedImage(t *testing.T) {
	audit := &OverlayAudit{DetectedEntities: []OverlayAuditEntity{{
		ID: "ent-italia", Type: "PERSON", Text: "Italia", CanonicalText: "Italia",
		SelectedForVideo: true, ImageStatus: "planned",
	}}}
	applyRequestedEntityImageContract(audit, nil, nil, true, true, []string{"PERSON"},
		map[string]struct{}{"anastasia trofimova": {}})

	require.True(t, audit.DetectedEntities[0].ImageRequired)
	require.Equal(t, 1, audit.EntityImages.Selected)
	require.Equal(t, 1, audit.EntityImages.Missing)
	require.Equal(t, EntityImageReasonUploadFailed, audit.EntityImages.MissingEntities[0].Reason)
}

// TestRequestedEntityImageContractAcceptsLocalVerifiedAssetWithoutDriveID
// covers the Kaufmann shape: the reviewed registry binds a local, hashed file
// to every scene, while the payload asks for Drive upload. The local bytes are
// a valid delivery and must not be reported as UPLOAD_FAILED just because the
// registry deliberately has no Drive locator.
func TestRequestedEntityImageContractAcceptsLocalVerifiedAssetWithoutDriveID(t *testing.T) {
	t.Setenv(requireEntityImagesEnv, "1")
	name := "Francis Kaufmann"
	entityID := auditPersonID(name)
	sha := strings.Repeat("a", 64)
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{{
			EntityID: entityID, Type: "PERSON", Text: name, CanonicalText: name,
			SceneID: "scene-1", Confidence: 1,
		}},
	}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "francis-local", SceneID: "scene-1", Kind: "entity_image", EntityID: entityID,
		AssetRefs: []capmodel.OverlayAssetRef{{
			AssetID: "local-asset", SHA256: sha, LocalPath: "/tmp/francis.jpg",
		}},
	}}}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	require.NotNil(t, audit)
	applyRequestedEntityImageContract(audit, bundle, plan, true, true, []string{"PERSON"})
	require.True(t, audit.DetectedEntities[0].ImageLocalVerified)
	require.False(t, audit.DetectedEntities[0].ImageDriveRequired)
	require.Equal(t, 1, audit.EntityImages.Selected)
	require.Equal(t, 1, audit.EntityImages.Bound)
	require.Equal(t, 0, audit.EntityImages.Missing)
	require.NoError(t, enforceEntityImageGate(audit))
}

// TestOverlayAuditBindsEntityCardImageWithoutEntityRef covers the plan shape
// the entity timeline emits: the card carries EntityID + Text, not an
// EntityRef. Once the reviewed registry binds bytes, the audit must report the
// card as planned/bound instead of a zero-image false negative.
func TestOverlayAuditBindsEntityCardImageWithoutEntityRef(t *testing.T) {
	entity := capabilityoverlay.ResolvedEntity{
		EntityID: "ent-villa", Type: "GPE", Text: "Villa Pamphili",
		CanonicalText: "Villa Pamphili", SceneID: "discovery", Confidence: 1,
	}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "villa-card", SceneID: "discovery", Kind: "entity_card",
		EntityID: "ent-villa", Text: "Villa Pamphili", ImagePresetID: "image_slide_left",
		AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-villa", SHA256: strings.Repeat("a", 64)}},
	}}}
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{entity},
		Assets:   []capabilityoverlay.BoundAsset{{EntityID: "ent-villa", AssetID: "asset-villa", Verified: true}},
	}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	require.NotNil(t, audit)
	require.NotNil(t, audit.EntityImages)
	require.Equal(t, 1, audit.EntityImages.BudgetUsed)
	require.Equal(t, 1, audit.EntityImages.Selected)
	require.Equal(t, 1, audit.EntityImages.Bound)
	require.Equal(t, 0, audit.EntityImages.Missing)
	require.Equal(t, "planned", audit.DetectedEntities[0].ImageStatus)
}

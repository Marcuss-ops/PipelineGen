package scriptgeneration

import (
	"encoding/json"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

// promotableChainPlan is the shared fixture of the three determinism tests: one
// mixed chain whose two entity-card segments both have a verified portrait.
func promotableChainPlan(planID string) (*capmodel.OverlayPlan, promotionEntity, promotionEntity) {
	person := promotionEntity{
		stable:    capabilityentities.StableEntityID("PERSON", "Steve Jobs"),
		canonical: "person:steve-jobs",
		name:      "Steve Jobs",
		url:       "https://drive.google.com/uc?export=download&id=steve",
	}
	org := promotionEntity{
		stable:    capabilityentities.StableEntityID("ORGANIZATION", "Apple"),
		canonical: "organization:apple",
		name:      "Apple",
		url:       "https://drive.google.com/uc?export=download&id=apple",
	}
	plan := &capmodel.OverlayPlan{
		PlanID: planID, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capmodel.OverlayItem{{
			ID: "ov-chain-1", SceneID: "scene-1",
			Kind: capmodel.KindMixedChain, TemplateID: capmodel.TemplateMixedChain,
			Text: person.name, StartUS: 1_000_000, DurationUS: 6_000_000,
			StartMs: 1000, EndMs: 7000,
			Params: map[string]any{
				"chain_segments": []any{
					map[string]any{
						"kind": string(capmodel.KindEntityCard), "text": person.name,
						"entity_id": person.stable, "motion_id": "card_reveal",
						"start_us": 1_000_000, "duration_us": 2_500_000,
					},
					map[string]any{
						"kind": string(capmodel.KindEntityCard), "text": org.name,
						"entity_id": org.stable, "motion_id": "card_reveal",
						"start_us": 3_900_000, "duration_us": 2_500_000,
					},
				},
			},
		}},
	}
	return plan, person, org
}

func marshalPlan(t *testing.T, plan *capmodel.OverlayPlan) string {
	t.Helper()
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	return string(raw)
}

// TestPromotionIsIdempotent proves the promotion pass is a projection and not an
// accumulating mutation: a second run over an already-promoted plan changes
// nothing, so a retry or a replayed durable run cannot drift the document.
func TestPromotionIsIdempotent(t *testing.T) {
	plan, person, org := promotableChainPlan("plan-idem")
	result := &GenerateResult{SemanticRenderBundle: promotionBundleForTwoEntities(t, person, org)}

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})
	once := marshalPlan(t, plan)

	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})
	twice := marshalPlan(t, plan)

	require.Equal(t, once, twice, "a second promotion pass changed the plan")
	segments, ok := capmodel.ChainSegmentMaps(plan.Items[0].Params)
	require.True(t, ok)
	require.Len(t, segments, 2, "promotion must keep both segments")
}

// TestPromotionFingerprintIsDeterministic is the ticket's acceptance criterion:
// the same request compiled twice yields the same plan fingerprint.
func TestPromotionFingerprintIsDeterministic(t *testing.T) {
	first, person, org := promotableChainPlan("plan-fp")
	promoteVerifiedEntityCards(first, &GenerateResult{
		SemanticRenderBundle: promotionBundleForTwoEntities(t, person, org),
	}, OverlayCanvasSpec{})

	second, person2, org2 := promotableChainPlan("plan-fp")
	promoteVerifiedEntityCards(second, &GenerateResult{
		SemanticRenderBundle: promotionBundleForTwoEntities(t, person2, org2),
	}, OverlayCanvasSpec{})

	fingerprint := first.FingerprintValue()
	require.NotEmpty(t, fingerprint)
	require.Equal(t, fingerprint, second.FingerprintValue(),
		"the same request produced two different plan fingerprints")
	require.Empty(t, first.Fingerprint, "FingerprintValue must not mutate the plan")

	// A promotion replay must not move the fingerprint either.
	promoteVerifiedEntityCards(first, &GenerateResult{
		SemanticRenderBundle: promotionBundleForTwoEntities(t, person, org),
	}, OverlayCanvasSpec{})
	require.Equal(t, fingerprint, first.FingerprintValue(),
		"a replayed promotion changed the plan fingerprint")
}

// TestEntityImageCeilingDropsOnlyEntityImages pins the declared post-seal drop:
// the run ceiling may remove entity images past its identity/count budget, and
// nothing else — order of the surviving items is the Rust order.
func TestEntityImageCeilingDropsOnlyEntityImages(t *testing.T) {
	entityImage := func(id, entityID string) capmodel.OverlayItem {
		return capmodel.OverlayItem{
			ID: id, SceneID: "scene-1", Kind: string(capmodel.KindEntityImage),
			TemplateID: "image_popup", EntityID: entityID,
			EntityRef: &capmodel.OverlayEntityRef{EntityID: entityID, CanonicalEntityID: entityID, Type: "PERSON"},
			AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: id}},
			StartUS:   0, DurationUS: 1_000_000,
		}
	}
	phrase := capmodel.OverlayItem{ID: "phrase-mid", SceneID: "scene-1", Kind: "text_phrase", Text: "in mezzo"}
	items := []capmodel.OverlayItem{
		entityImage("a", "person:a"),
		phrase,
		entityImage("b", "person:b"),
		entityImage("b-dup", "person:b"),
		entityImage("c", "person:c"),
	}

	capped := capEntityImageOverlays(items, 2)
	require.Len(t, capped, 3, "one phrase + two entity images inside the ceiling")
	require.Equal(t, []string{"a", "phrase-mid", "b"},
		[]string{capped[0].ID, capped[1].ID, capped[2].ID},
		"the ceiling must drop entity images only and keep the Rust order")
	require.Equal(t, "text_phrase", capped[1].Kind)

	// Determinism: the same input yields the same survivors every time.
	again := capEntityImageOverlays(items, 2)
	require.Equal(t, []string{"a", "phrase-mid", "b"},
		[]string{again[0].ID, again[1].ID, again[2].ID})

	// A zero ceiling is "no ceiling", not "drop everything".
	require.Len(t, capEntityImageOverlays(items, 0), len(items))
}

package scriptgeneration

import (
	"encoding/json"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

// The post-seal mutation surface of one chain segment, declared in
// docs/architecture/OVERLAY-EDITORIAL-OWNERSHIP.md (ADR-029 decision matrix).
// Entity-card promotion is the ONE exception that may rewrite a segment's
// identity, because whether a card has a certified portrait is known only to
// the PipelineGen media plane, after Rust ranked it.
var (
	chainSegmentPromotableAddedKeys = []string{
		"kind", "template_id", "preset_id", "image_preset_id",
		"entity_caption", "asset_id", "assets",
	}
	chainSegmentPromotableDroppedKeys = []string{
		"text", "entity_style_id", "motion_id", "motion_params", "params",
	}
)

func chainSegmentKeySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

func cloneChainSegments(t *testing.T, item capmodel.OverlayItem) []map[string]any {
	t.Helper()
	segments, ok := capmodel.ChainSegmentMaps(item.Params)
	require.True(t, ok, "chain item %q carries no chain_segments", item.ID)
	raw, err := json.Marshal(segments)
	require.NoError(t, err)
	var cloned []map[string]any
	require.NoError(t, json.Unmarshal(raw, &cloned))
	return cloned
}

// promotionBundleForTwoEntities returns a bundle whose verified media covers
// both entity cards of the chain under test.
func promotionBundleForTwoEntities(t *testing.T, first, second promotionEntity) *capabilityoverlay.SemanticRenderBundleV1 {
	t.Helper()
	bundle := promotionTestBundle(first.stable, first.canonical, first.name, first.url)
	bundle.Entities = append(bundle.Entities, capabilityoverlay.ResolvedEntity{
		EntityID: second.stable, CanonicalEntityID: second.canonical, Type: "PERSON",
		Text: second.name, CanonicalText: second.name, SceneID: "scene-1",
	})
	bundle.Assets = append(bundle.Assets, capabilityoverlay.BoundAsset{
		EntityID: second.stable, AssetID: "asset-org",
		ContentHash: "b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0",
		SourceURL:   second.url, Verified: true,
	})
	return bundle
}

type promotionEntity struct {
	stable    string
	canonical string
	name      string
	url       string
}

func TestChainSegmentPromotionTouchesOnlyTheDeclaredSurface(t *testing.T) {
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
		PlanID: "plan-chain-2",
		Items: []capmodel.OverlayItem{{
			ID: "ov-chain-2", SceneID: "scene-1",
			Kind: capmodel.KindMixedChain, TemplateID: capmodel.TemplateMixedChain,
			Text: person.name, StartUS: 1_000_000, DurationUS: 6_000_000,
			StartMs: 1000, EndMs: 7000, PresetID: "",
			Params: map[string]any{
				"chain_segments": []any{
					map[string]any{
						"kind": string(capmodel.KindEntityCard), "text": person.name,
						"entity_id": person.stable, "motion_id": "card_reveal",
						"motion_params":   map[string]any{"enter_frames": 17},
						"entity_style_id": "premium_below_random_v1",
						"start_us":        1_000_000, "duration_us": 2_500_000,
						"group_id": "group-a", "camera_motion_id": "camera_push",
					},
					map[string]any{
						"kind": string(capmodel.KindEntityCard), "text": org.name,
						"entity_id": org.stable, "motion_id": "card_reveal",
						"entity_style_id": "premium_below_random_v1",
						"start_us":        3_900_000, "duration_us": 2_500_000,
						"group_id": "group-b", "camera_motion_id": "camera_push",
					},
				},
			},
		}},
	}
	before := cloneChainSegments(t, plan.Items[0])
	beforeItem := plan.Items[0]

	result := &GenerateResult{SemanticRenderBundle: promotionBundleForTwoEntities(t, person, org)}
	promoteVerifiedEntityCards(plan, result, OverlayCanvasSpec{})

	require.Len(t, plan.Items, 1, "promotion must not add or remove plan items")
	item := plan.Items[0]

	// Identity and timing stay Rust's: only the chain family flips, because
	// every segment became an image.
	require.Equal(t, beforeItem.ID, item.ID)
	require.Equal(t, beforeItem.SceneID, item.SceneID)
	require.Equal(t, beforeItem.StartUS, item.StartUS)
	require.Equal(t, beforeItem.DurationUS, item.DurationUS)
	require.Equal(t, beforeItem.StartMs, item.StartMs)
	require.Equal(t, beforeItem.EndMs, item.EndMs)
	require.Equal(t, capmodel.KindImageChain, item.Kind)
	require.Equal(t, capmodel.TemplateImageChain, item.TemplateID)
	require.Empty(t, item.Text, "the declared exception clears the legacy chain text")

	after := cloneChainSegments(t, item)
	require.Len(t, after, len(before), "promotion must not add or remove segments")

	added := chainSegmentKeySet(chainSegmentPromotableAddedKeys)
	dropped := chainSegmentKeySet(chainSegmentPromotableDroppedKeys)

	for index := range before {
		for key, value := range after[index] {
			if _, ok := before[index][key]; !ok {
				require.Truef(t, added[key],
					"segment %d gained undeclared key %q", index, key)
				continue
			}
			if added[key] {
				continue
			}
			require.Equalf(t, before[index][key], value,
				"segment %d changed key %q outside the declared promotion surface", index, key)
		}
		for key := range before[index] {
			if _, ok := after[index][key]; ok {
				continue
			}
			require.Truef(t, dropped[key],
				"segment %d dropped undeclared key %q", index, key)
		}
		// Absolute timing is Rust's and must survive verbatim.
		require.Equal(t, before[index]["start_us"], after[index]["start_us"])
		require.Equal(t, before[index]["duration_us"], after[index]["duration_us"])
		require.Equal(t, before[index]["entity_id"], after[index]["entity_id"])
		require.Equal(t, string(capmodel.KindEntityImage), after[index]["kind"])
	}
}

func TestChainSegmentPresetResolutionTouchesOnlyPresetID(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "ov-chain-3", SceneID: "scene-1",
		Kind: capmodel.KindPhraseChain, TemplateID: capmodel.TemplatePhraseChain,
		Params: map[string]any{
			"chain_segments": []any{
				map[string]any{
					"kind": "text_phrase", "template_id": "IMPORTANT_PHRASE",
					"text": "primo", "motion_id": "phrase_default",
					"start_us": 0, "duration_us": 1_500_000,
				},
				map[string]any{
					"kind": "text_phrase", "template_id": "IMPORTANT_PHRASE",
					"text": "secondo", "motion_id": "phrase_default",
					"start_us": 1_900_000, "duration_us": 1_500_000,
				},
			},
		},
	}}}
	before := cloneChainSegments(t, plan.Items[0])

	applyChainSegmentPresets(plan)

	after := cloneChainSegments(t, plan.Items[0])
	require.Len(t, after, len(before))
	for index := range before {
		for key, value := range before[index] {
			require.Equalf(t, value, after[index][key],
				"segment %d changed key %q: preset resolution may only set preset_id", index, key)
		}
		require.NotEmptyf(t, after[index]["preset_id"],
			"segment %d kept no preset after resolution", index)
	}
	require.Equal(t, after[0]["preset_id"], plan.Items[0].PresetID,
		"the chain mirrors its first segment preset for legacy consumers")
}

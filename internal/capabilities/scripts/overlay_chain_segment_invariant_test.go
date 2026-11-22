package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

// Entity-card → entity-image promotion moved to the v2 planner (CHAIN-SEGMENT
// v2). The Go seal boundary now adopts the plan as it arrives: this test pins
// that no item, segment or identity is rewritten after the planner sealed.
func TestSealBoundaryLeavesEveryItemAndChainSegmentUntouched(t *testing.T) {
	plan := capmodel.OverlayPlan{
		PlanID: "plan-chain-v2", VideoID: "video-chain-v2",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capmodel.OverlayItem{
			{
				ID: "scene-1:chain-image:0", SceneID: "scene-1",
				Kind: capmodel.KindImageChain, TemplateID: capmodel.TemplateImageChain,
				StartUS: 1_000_000, DurationUS: 6_000_000, StartMs: 1_000, EndMs: 7_000,
				PresetID: "image_slide_left",
				Params: map[string]any{
					"chain_segments": []any{
						map[string]any{
							"kind": string(capmodel.KindEntityImage), "template_id": "image_popup",
							"preset_id": "image_slide_left", "image_preset_id": "image_scale_in",
							"entity_caption": "Ada Lovelace", "entity_id": "person-ada",
							"asset_id": "asset-ada",
							"assets": []any{map[string]any{
								"asset_id": "asset-ada", "url": "https://cdn.example/ada.jpg",
								"sha256": strings.Repeat("1", 64),
							}},
							"start_us": 1_000_000, "duration_us": 2_500_000,
						},
						map[string]any{
							"kind": string(capmodel.KindEntityCard), "template_id": "person_default",
							"text": "Grace Hopper", "entity_id": "person-grace",
							"motion_id": "card_reveal", "start_us": 3_900_000, "duration_us": 2_500_000,
						},
					},
					"chain_motions": []any{"image_focus_reveal", "card_reveal"},
					"chain_count":   2,
					"chain_gap_us":  400_000,
				},
			},
			{
				ID: "card-grace", SceneID: "scene-1",
				Kind: string(capmodel.KindEntityCard), TemplateID: "person_default",
				Text: "Grace Hopper", EntityID: "person-grace",
				StartUS: 8_000_000, DurationUS: 2_500_000, StartMs: 8_000, EndMs: 10_500,
			},
		},
	}
	before, err := json.Marshal(plan.Items)
	require.NoError(t, err)

	sealed, err := sealCompiledOverlayPlan(&plan, &GenerateResult{}, Language("it"), nil, OverlayCanvasSpec{})
	require.NoError(t, err)

	// `render_key` is the one derived field the seal stamps; every planner-owned
	// byte must survive it.
	for i := range sealed.Items {
		sealed.Items[i].RenderKey = ""
	}
	after, err := json.Marshal(sealed.Items)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after),
		"the seal boundary rewrote the plan the planner sealed")

	// The two identities are still the planner's: a card segment stays a card
	// and a standalone card is not flipped to entity_image.
	segments, ok := sealed.Items[0].Segments()
	require.True(t, ok)
	require.Equal(t, string(capmodel.KindEntityCard), segments[1].Kind)
	require.Equal(t, string(capmodel.KindEntityCard), sealed.Items[1].Kind)
	require.Empty(t, sealed.Items[1].AssetRefs, "no promotion ran after the seal")
}

func cloneChainSegments(t *testing.T, item capmodel.OverlayItem) []map[string]any {
	t.Helper()
	segments, ok := item.Segments()
	require.True(t, ok, "chain item %q carries no chain_segments", item.ID)
	raw, err := json.Marshal(segments)
	require.NoError(t, err)
	var cloned []map[string]any
	require.NoError(t, json.Unmarshal(raw, &cloned))
	return cloned
}

// Preset resolution after adoption may only fill preset_id: it is the one
// derived attribute, and it never re-selects identity or motion.
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

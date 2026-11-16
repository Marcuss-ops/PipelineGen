package scriptgeneration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const overlayPlanContractFixtureDir = "../../../../BrainRust/overlay-compiler/tests/fixtures/overlay_plan"

// TestOverlayPlanV2AllFixturesMatchFrozenGoldens is the post-cutover WP5 gate.
// The deleted Go planner can no longer serve as an independent oracle, so the
// checked-in plans are the frozen contract and this test verifies that the real
// production adapter returns exactly those item decisions for every fixture:
// phrases, maps, verified/unverified entity cards, editorial/chart/recap, the
// localized run, general phrase/image/mixed chains and a map collision resolved
// before chain fusion.
func TestOverlayPlanV2AllFixturesMatchFrozenGoldens(t *testing.T) {
	compiler := newRustOverlayPlanFixtureCompiler(t)
	if compiler == nil {
		t.Skip("semantic-engine binary not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH to verify the v2 fixture contract")
	}
	fixtures := []string{
		"minimal_phrase_only",
		"maps_trim_overlap",
		"entity_cards_verified_media",
		"editorial_toggles_charts_recap",
		"localized_short_phrase_empty",
		"recap_and_light_leaks",
		"general_chain_rendering",
		"chain_collision_resolution",
	}
	for _, name := range fixtures {
		name := name
		t.Run(name, func(t *testing.T) {
			request, err := os.ReadFile(filepath.Join(overlayPlanContractFixtureDir, "requests", name+".request.json"))
			require.NoError(t, err)
			rawGolden, err := os.ReadFile(filepath.Join(overlayPlanContractFixtureDir, "plans", name+".plan.json"))
			require.NoError(t, err)

			var golden struct {
				OK   bool `json:"ok"`
				Plan struct {
					SchemaVersion string `json:"schema_version"`
					PlanID        string `json:"plan_id"`
					VideoID       string `json:"video_id"`
					Items         []struct {
						ID            string          `json:"id"`
						SceneID       string          `json:"scene_id"`
						Kind          string          `json:"kind"`
						TemplateID    string          `json:"template_id"`
						Text          string          `json:"text"`
						StartMS       int64           `json:"start_ms"`
						EndMS         int64           `json:"end_ms"`
						StartUS       int64           `json:"start_us"`
						DurationUS    int64           `json:"duration_us"`
						MotionID      string          `json:"motion_id"`
						ImagePresetID string          `json:"image_preset_id"`
						EntityID      string          `json:"entity_id"`
						EntityCaption string          `json:"entity_caption"`
						Map           json.RawMessage `json:"map"`
						AssetRefs     []struct {
							AssetID string `json:"asset_id"`
							SHA256  string `json:"sha256"`
						} `json:"asset_refs"`
					} `json:"items"`
				} `json:"plan"`
			}
			require.NoError(t, json.Unmarshal(rawGolden, &golden))
			require.True(t, golden.OK, "golden is not an ok response")
			require.Equal(t, "editorial_plan.v2", golden.Plan.SchemaVersion)
			require.NotEmpty(t, golden.Plan.Items, "golden carries no items")

			live, err := compiler.CompileOverlayPlanV2(context.Background(), request)
			require.NoError(t, err)
			require.NotNil(t, live)
			require.Equal(t, golden.Plan.PlanID, live.PlanID)
			require.Equal(t, golden.Plan.VideoID, live.VideoID)
			require.Len(t, live.Items, len(golden.Plan.Items), "item count drifted from the frozen contract")

			for index, want := range golden.Plan.Items {
				got := live.Items[index]
				label := "item " + want.ID
				require.Equal(t, want.ID, got.ID, label)
				require.Equal(t, want.SceneID, got.SceneID, label)
				require.Equal(t, want.Kind, got.Kind, label)
				require.Equal(t, want.TemplateID, got.TemplateID, label)
				require.Equal(t, want.Text, got.Text, label)
				require.Equal(t, want.MotionID, got.MotionID, label)
				require.Equal(t, want.ImagePresetID, got.ImagePresetID, label)
				require.Equal(t, want.EntityID, got.EntityID, label)
				require.Equal(t, want.EntityCaption, got.EntityCaption, label)
				wantStart, wantDuration := frozenWindow(want.StartUS, want.DurationUS, want.StartMS, want.EndMS)
				gotStart, gotDuration := frozenWindow(got.StartUS, got.DurationUS, got.StartMs, got.EndMs)
				require.Equal(t, wantStart, gotStart, label+" start")
				require.Equal(t, wantDuration, gotDuration, label+" duration")
				require.Equal(t, len(want.Map) > 0 && string(want.Map) != "null", got.Map != nil, label+" map presence")
				require.Len(t, got.AssetRefs, len(want.AssetRefs), label+" asset refs")
				for refIndex, wantRef := range want.AssetRefs {
					require.Equal(t, wantRef.AssetID, got.AssetRefs[refIndex].AssetID, label+" asset id")
					require.Equal(t, wantRef.SHA256, got.AssetRefs[refIndex].SHA256, label+" asset sha256")
				}
			}
		})
	}
}

func frozenWindow(startUS, durationUS, startMS, endMS int64) (int64, int64) {
	if durationUS > 0 {
		return startUS, durationUS
	}
	return startMS * 1000, (endMS - startMS) * 1000
}

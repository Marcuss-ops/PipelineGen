package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

// A mixed chain is fused before the registry pass. The entity card inside it
// must receive its reviewed portrait there; there may be no standalone donor
// card anywhere else in the plan.
func TestApplyVerifiedEntityAssetsBindsPortraitInsideMixedChain(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "anastasia.png")
	imageBytes, sha := preflightPNGBytes(t)
	require.NoError(t, os.WriteFile(imagePath, imageBytes, 0o600))

	manifest := map[string]any{
		"version": 1,
		"entities": map[string]any{
			"Anastasia Trofimova": []any{map[string]any{
				"asset_id": sha, "sha256": sha, "path": "anastasia.png", "media_type": "image/png",
			}},
		},
	}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	registryPath := filepath.Join(dir, "entity_asset_registry.json")
	require.NoError(t, os.WriteFile(registryPath, raw, 0o600))
	t.Setenv("VELOX_ENTITY_ASSET_REGISTRY", registryPath)

	chain := capmodel.OverlayItem{
		ID: "victims-chain", SceneID: "victims-context", Kind: "mixed_chain", TemplateID: "MIXED_CHAIN",
		Params: map[string]any{"chain_segments": []any{
			map[string]any{"kind": "text_phrase", "text": "Prima di parlare dell'imputato"},
			map[string]any{
				"kind": "entity_card", "entity_id": "ent_anastasia", "text": "Anastasia Trofimova",
				"image_preset_id": "image_scale_in",
			},
		}},
	}
	got := applyVerifiedEntityAssets([]capmodel.OverlayItem{chain}, "plan-chain")
	require.Len(t, got, 1)
	require.Len(t, got[0].AssetRefs, 1)
	require.Equal(t, sha, got[0].AssetRefs[0].SHA256)

	segments := chainSegments(got[0])
	require.Len(t, segments, 2)
	require.Empty(t, segments[0]["assets"], "phrase segment must not acquire an identity image")
	require.Equal(t, sha, segments[1]["asset_id"])
	assets, ok := segments[1]["assets"].([]any)
	require.True(t, ok)
	require.Len(t, assets, 1)
}

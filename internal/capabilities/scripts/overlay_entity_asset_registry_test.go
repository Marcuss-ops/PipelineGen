package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

// TestRegistryDriveLocatorFeedsAudit pins the end-to-end contract: a reviewed
// registry entry with a real Drive file id must produce a downloadable URL on
// the plan asset, and the audit must treat that card as bound (no local-only
// bypass, no UPLOAD_FAILED, no missing entity image).
func TestRegistryDriveLocatorFeedsAudit(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "francis.png")
	imageBytes, sha := preflightPNGBytes(t)
	require.NoError(t, os.WriteFile(imagePath, imageBytes, 0o600))

	manifest := map[string]any{
		"version": 1,
		"entities": map[string]any{
			"Francis Kaufmann": []any{map[string]any{
				"asset_id":      sha,
				"sha256":        sha,
				"path":          "francis.png",
				"media_type":    "image/png",
				"drive_file_id": "drive-francis-1",
				"drive_link":    "https://drive.google.com/file/d/drive-francis-1/view",
			}},
		},
	}
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	registryPath := filepath.Join(dir, "entity_asset_registry.json")
	require.NoError(t, os.WriteFile(registryPath, raw, 0o600))
	t.Setenv("VELOX_ENTITY_ASSET_REGISTRY", registryPath)

	const name = "Francis Kaufmann"
	entityID := auditPersonID(name)
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{{
		ID: "francis-image", SceneID: "scene-1", Kind: "entity_image",
		EntityID: entityID, EntityCaption: name,
	}}}
	plan.Items = applyVerifiedEntityAssets(plan.Items, "plan-registry-drive")
	require.Len(t, plan.Items, 1)
	require.Len(t, plan.Items[0].AssetRefs, 1)
	require.Equal(t, "https://drive.google.com/uc?export=download&id=drive-francis-1", plan.Items[0].AssetRefs[0].URL)
	require.Equal(t, imagePath, plan.Items[0].AssetRefs[0].LocalPath)

	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{{
			EntityID: entityID, Type: "PERSON", Text: name, CanonicalText: name,
			SceneID: "scene-1", Confidence: 1,
		}},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: entityID, AssetID: "bundle-asset",
			ContentHash: strings.Repeat("b", 64), SourceURL: "https://example.test/bundle.jpg", Verified: true,
		}},
	}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	require.NotNil(t, audit)
	require.NotNil(t, audit.EntityImages)
	applyRequestedEntityImageContract(audit, bundle, plan, true, true, []string{"PERSON"})

	require.Len(t, audit.DetectedEntities, 1)
	require.Equal(t, "drive-francis-1", audit.DetectedEntities[0].ImageDriveFileID)
	require.True(t, audit.DetectedEntities[0].ImageDriveRequired)
	require.Equal(t, "planned", audit.DetectedEntities[0].ImageStatus)
	require.Equal(t, 1, audit.EntityImages.Selected)
	require.Equal(t, 1, audit.EntityImages.Bound)
	require.Equal(t, 0, audit.EntityImages.Missing)
	require.Empty(t, audit.EntityImages.MissingEntities)
	require.Equal(t, 1, audit.EntityImages.Reasons[EntityImageReasonBound])
	require.NoError(t, enforceEntityImageGate(audit))
}

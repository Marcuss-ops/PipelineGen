package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/stretchr/testify/require"
)

// TestOverlayAuditBindsEntityImageNestedInChain pins the production shape: the
// overlay plan coalesces entity images into an image_chain item, and the entity
// binding lives inside params.chain_segments. The audit must count that image
// as bound instead of reporting BINDING_MISSING just because the top-level item
// has no EntityRef.
func TestOverlayAuditBindsEntityImageNestedInChain(t *testing.T) {
	const (
		name     = "Ana Rossi"
		sha      = "a82a3ceb69b48bd39ed2fc146da00ef058c396a97d2866bcf57dd625b1e0bb54"
		driveURL = "https://drive.google.com/uc?export=download&id=drive-file-1"
	)
	entityID := auditPersonID(name)
	chain := capmodel.OverlayItem{
		ID: "chain-1", SceneID: "scene-1", Kind: "image_chain", TemplateID: "IMAGE_CHAIN",
		AssetRefs: []capmodel.OverlayAssetRef{{AssetID: sha, URL: driveURL, SHA256: sha}},
		Params: map[string]any{
			"chain_segments": []any{
				map[string]any{
					"kind":           "entity_image",
					"entity_id":      entityID,
					"entity_caption": name,
					"asset_id":       sha,
					"assets": []any{
						map[string]any{"asset_id": sha, "sha256": sha, "url": driveURL},
					},
				},
			},
		},
	}
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{chain}}
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Entities: []capabilityoverlay.ResolvedEntity{auditTestEntity(name)},
		Assets: []capabilityoverlay.BoundAsset{{
			EntityID: entityID, AssetID: sha, ContentHash: sha, SourceURL: driveURL, Verified: true,
		}},
	}

	audit := buildOverlayAuditWithLineage(bundle, plan, nil)
	require.NotNil(t, audit)
	require.NotNil(t, audit.EntityImages)
	applyRequestedEntityImageContract(audit, bundle, plan, true, true, []string{"PERSON"})

	require.Equal(t, 1, audit.EntityImages.Selected)
	require.Equal(t, 1, audit.EntityImages.Bound)
	require.Equal(t, 0, audit.EntityImages.Missing)
	require.Equal(t, 1, audit.EntityImages.Reasons[EntityImageReasonBound])
	require.Empty(t, audit.EntityImages.MissingEntities)
	require.Equal(t, "drive-file-1", audit.DetectedEntities[0].ImageDriveFileID)
	require.Equal(t, "planned", audit.DetectedEntities[0].ImageStatus)
	require.NoError(t, enforceEntityImageGate(audit))
}

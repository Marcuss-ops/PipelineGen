package scriptgeneration

import (
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// overlayPlannedEntityImage is the plan's own entity-image binding. The audit
// must read this, not the annotation status: a chain item nests its entity
// segments under params.chain_segments, where the top-level item has no
// EntityRef and therefore cannot be joined by the old audit scan.
type overlayPlannedEntityImage struct {
	SceneID     string
	EntityID    string
	AssetID     string
	SHA256      string
	DriveFileID string
}

func overlayPlanEntityImageIndex(plan *capmodel.OverlayPlan) map[string]overlayPlannedEntityImage {
	index := make(map[string]overlayPlannedEntityImage)
	if plan == nil {
		return index
	}
	for _, item := range plan.Items {
		if strings.EqualFold(strings.TrimSpace(item.Kind), string(capmodel.KindEntityImage)) {
			if binding, ok := overlayPlanBindingFromAssetRefs(item.AssetRefs, ""); ok {
				overlayAddPlannedEntityImage(index, item.SceneID, item.EntityID, binding)
			}
		}
		for _, layer := range item.ImageLayers {
			if strings.TrimSpace(layer.EntityID) == "" {
				continue
			}
			if binding, ok := overlayPlanBindingFromAssetRefs(item.AssetRefs, layer.AssetID); ok {
				overlayAddPlannedEntityImage(index, item.SceneID, layer.EntityID, binding)
			}
		}
		segments, _ := item.Segments()
		for _, segment := range segments {
			if !strings.EqualFold(strings.TrimSpace(segment.Kind), "entity_image") {
				continue
			}
			entityID := strings.TrimSpace(segment.EntityID)
			if entityID == "" {
				continue
			}
			if binding, ok := overlayPlanBindingFromChainSegment(segment); ok {
				overlayAddPlannedEntityImage(index, item.SceneID, entityID, binding)
			}
		}
	}
	return index
}

func overlayPlannedEntityImageKey(sceneID, entityID string) string {
	return strings.TrimSpace(sceneID) + "::" + strings.TrimSpace(entityID)
}

func overlayAddPlannedEntityImage(index map[string]overlayPlannedEntityImage, sceneID, entityID string, binding overlayPlannedEntityImage) {
	entityID = strings.TrimSpace(entityID)
	if entityID == "" {
		return
	}
	binding.SceneID = strings.TrimSpace(sceneID)
	binding.EntityID = entityID
	if binding.AssetID == "" && binding.SHA256 == "" && binding.DriveFileID == "" {
		return
	}
	sceneKey := overlayPlannedEntityImageKey(sceneID, entityID)
	if existing, ok := index[sceneKey]; !ok || (existing.DriveFileID == "" && binding.DriveFileID != "") {
		index[sceneKey] = binding
	}
	// Run-level fallback: a scene without its own binding can still use the
	// first planned occurrence of the same identity instead of degrading to a
	// text card. Scene-scoped entries remain authoritative.
	if _, ok := index[entityID]; !ok {
		index[entityID] = binding
	}
}

func overlayPlannedEntityImageFor(index map[string]overlayPlannedEntityImage, sceneID, entityID string) (overlayPlannedEntityImage, bool) {
	if binding, ok := index[overlayPlannedEntityImageKey(sceneID, entityID)]; ok {
		return binding, true
	}
	binding, ok := index[strings.TrimSpace(entityID)]
	return binding, ok
}

func overlayPlanBindingFromAssetRefs(refs []capmodel.OverlayAssetRef, assetID string) (overlayPlannedEntityImage, bool) {
	for _, ref := range refs {
		if assetID != "" && ref.AssetID != assetID && ref.SHA256 != assetID {
			continue
		}
		sha := strings.TrimSpace(ref.SHA256)
		if sha == "" && overlayLooksLikeSHA256(ref.AssetID) {
			sha = strings.TrimSpace(ref.AssetID)
		}
		binding := overlayPlannedEntityImage{
			AssetID:     strings.TrimSpace(ref.AssetID),
			SHA256:      sha,
			DriveFileID: overlayPlanDriveFileID(ref.URL),
		}
		if binding.AssetID == "" {
			binding.AssetID = sha
		}
		return binding, true
	}
	return overlayPlannedEntityImage{}, false
}

func overlayPlanBindingFromChainSegment(segment capmodel.ChainSegment) (overlayPlannedEntityImage, bool) {
	if len(segment.Assets) == 0 {
		if assetID := strings.TrimSpace(segment.AssetID); assetID != "" {
			return overlayPlannedEntityImage{AssetID: assetID}, true
		}
		return overlayPlannedEntityImage{}, false
	}
	asset := segment.Assets[0]
	sha := strings.TrimSpace(asset.SHA256)
	assetID := firstNonEmptyAuditValue(strings.TrimSpace(asset.AssetID), strings.TrimSpace(segment.AssetID), sha)
	return overlayPlannedEntityImage{
		AssetID:     assetID,
		SHA256:      sha,
		DriveFileID: overlayPlanDriveFileID(strings.TrimSpace(asset.URL)),
	}, true
}

func overlayLooksLikeSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// overlayPlanDriveFileID resolves the Drive identity a plan/audit image is
// addressable by. The canonical parser owns every accepted shape, so this
// projection cannot accept a URL the rest of the pipeline would reject.
func overlayPlanDriveFileID(raw string) string {
	id, err := urlutil.FileIDFromDriveLink(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

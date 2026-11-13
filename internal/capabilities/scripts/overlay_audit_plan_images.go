package scriptgeneration

import (
	"encoding/json"
	"net/url"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
	LocalPath   string
	ProviderURL string
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
		for _, segment := range overlayPlanChainSegments(item) {
			if !strings.EqualFold(overlayPlanString(segment, "kind"), "entity_image") {
				continue
			}
			entityID := overlayPlanString(segment, "entity_id")
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
	if binding.AssetID == "" && binding.SHA256 == "" && binding.DriveFileID == "" && binding.LocalPath == "" {
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
			LocalPath:   strings.TrimSpace(ref.LocalPath),
			ProviderURL: strings.TrimSpace(ref.URL),
		}
		if binding.AssetID == "" {
			binding.AssetID = sha
		}
		return binding, true
	}
	return overlayPlannedEntityImage{}, false
}

func overlayPlanBindingFromChainSegment(segment map[string]any) (overlayPlannedEntityImage, bool) {
	var assets []map[string]any
	if raw, ok := segment["assets"]; ok {
		encoded, err := json.Marshal(raw)
		if err == nil {
			_ = json.Unmarshal(encoded, &assets)
		}
	}
	if len(assets) == 0 {
		rawURL := firstNonEmptyAuditValue(overlayPlanString(segment, "url"), overlayPlanString(segment, "source_url"))
		if rawURL == "" {
			return overlayPlannedEntityImage{}, false
		}
		return overlayPlannedEntityImage{
			AssetID:     overlayPlanString(segment, "asset_id"),
			DriveFileID: overlayPlanDriveFileID(rawURL),
			LocalPath:   overlayPlanString(segment, "local_path"),
			ProviderURL: rawURL,
		}, true
	}
	asset := assets[0]
	sha := firstNonEmptyAuditValue(overlayPlanString(asset, "sha256"), overlayPlanString(asset, "content_hash"))
	assetID := firstNonEmptyAuditValue(overlayPlanString(asset, "asset_id"), overlayPlanString(segment, "asset_id"), sha)
	rawURL := firstNonEmptyAuditValue(overlayPlanString(asset, "url"), overlayPlanString(asset, "source_url"), overlayPlanString(segment, "url"))
	driveID := firstNonEmptyAuditValue(overlayPlanString(asset, "drive_file_id"), overlayPlanString(segment, "drive_file_id"), overlayPlanDriveFileID(rawURL))
	localPath := firstNonEmptyAuditValue(overlayPlanString(asset, "local_path"), overlayPlanString(segment, "local_path"))
	return overlayPlannedEntityImage{
		AssetID:     assetID,
		SHA256:      sha,
		DriveFileID: driveID,
		LocalPath:   localPath,
		ProviderURL: rawURL,
	}, true
}

func overlayPlanChainSegments(item capmodel.OverlayItem) []map[string]any {
	raw, ok := item.Params["chain_segments"]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var segments []map[string]any
	if err := json.Unmarshal(encoded, &segments); err != nil {
		return nil
	}
	return segments
}

func overlayPlanString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
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

func overlayPlanDriveFileID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err == nil {
		if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
			return id
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "d" {
				return parts[i+1]
			}
		}
	}
	if !strings.Contains(raw, "://") && !strings.ContainsAny(raw, "/?") {
		return raw
	}
	return ""
}

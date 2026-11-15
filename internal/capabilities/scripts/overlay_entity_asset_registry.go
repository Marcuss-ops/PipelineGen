package scriptgeneration

import (
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// loadVerifiedEntityAssets adapts the entitycatalog-owned registry to the
// overlay reference the presentation layer carries. The registry policy
// (paths, SHA-256 re-verification, Drive locator, key normalization) lives in
// entitycatalog; this package only binds the verified entries to overlay items.
func loadVerifiedEntityAssets() (map[string][]capmodel.OverlayAssetRef, error) {
	assets, err := entitycatalog.LoadVerifiedAssetRegistry()
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, nil
	}
	registry := make(map[string][]capmodel.OverlayAssetRef, len(assets))
	for key, entries := range assets {
		for _, entry := range entries {
			registry[key] = append(registry[key], capmodel.OverlayAssetRef{
				AssetID:   entry.AssetID,
				URL:       entry.URL,
				SHA256:    entry.SHA256,
				MediaType: entry.MediaType,
				LocalPath: entry.LocalPath,
			})
		}
	}
	return registry, nil
}

// overlayItemWantsCertifiedImage limits registry binding to cards that already
// intend to render an image. A plain text card must not acquire one just
// because its identity is registered.
func overlayItemWantsCertifiedImage(item capmodel.OverlayItem) bool {
	switch strings.TrimSpace(item.Kind) {
	case "entity_image":
		return true
	case "entity_card", "location", "organization":
		return strings.TrimSpace(item.ImagePresetID) != "" || len(item.AssetRefs) > 0
	default:
		return false
	}
}

func overlayItemEntityName(item capmodel.OverlayItem) string {
	if caption := entityCaptionOf(item); caption != "" {
		return caption
	}
	return strings.TrimSpace(item.Text)
}

func entityCaptionOf(item capmodel.OverlayItem) string {
	if caption := strings.TrimSpace(item.EntityCaption); caption != "" {
		return caption
	}
	if item.EntityRef != nil {
		return strings.TrimSpace(item.EntityRef.Name)
	}
	return ""
}

// applyVerifiedEntityAssets binds only reviewed local files. If a plan names
// at least one registered identity, the registry becomes authoritative for
// that plan: an unregistered named portrait is demoted to a text card instead
// of falling back to an unrelated search result. Plans with no registered
// identity are left untouched, so this local editorial registry cannot change
// unrelated jobs.
func applyVerifiedEntityAssets(items []capmodel.OverlayItem, planID string) ([]capmodel.OverlayItem, error) {
	registry, err := loadVerifiedEntityAssets()
	if err != nil {
		return nil, fmt.Errorf("verified entity asset registry: %w", err)
	}
	if len(registry) == 0 || len(items) == 0 {
		return items, nil
	}
	if !planUsesRegisteredEntity(items, registry) {
		return items, nil
	}
	seen := map[string]int{}
	out := make([]capmodel.OverlayItem, 0, len(items))
	for _, item := range items {
		switch strings.TrimSpace(item.Kind) {
		case "entity_image", "entity_card", "location", "organization":
			// The planner emits image-bearing entity cards under several
			// kinds. The reviewed registry is the semantic gate for all of
			// them; text-only cards stay untouched.
			if !overlayItemWantsCertifiedImage(item) {
				out = append(out, item)
				continue
			}
			name := overlayItemEntityName(item)
			key := entitycatalog.NormalizeVerifiedAssetKey(name)
			refs := registry[key]
			if len(refs) == 0 {
				// The local registry only overrides identities it reviewed. A
				// content-addressed asset already produced by the semantic bundle
				// or the runtime media stage is verified bytes and must render;
				// demoting it here made every bundle-only identity (AP, ANSA,
				// locations) lose its image whenever one registered name was
				// present in the same plan.
				if strings.TrimSpace(item.Kind) == "entity_image" && !overlayItemHasVerifiedIdentityAsset(item) {
					out = append(out, verifiedEntityTextCard(item, name, planID))
					continue
				}
				out = append(out, item)
				continue
			}
			ref := refs[seen[key]%len(refs)]
			seen[key]++
			item.AssetRefs = []capmodel.OverlayAssetRef{ref}
			item.RenderKey = ""
			out = append(out, item)
		case "image_chain", "mixed_chain", "phrase_chain", "map_chain":
			out = append(out, bindVerifiedChainAssets(item, registry, seen))
		default:
			out = append(out, item)
		}
	}
	return out, nil
}

func planUsesRegisteredEntity(items []capmodel.OverlayItem, registry map[string][]capmodel.OverlayAssetRef) bool {
	for _, item := range items {
		if overlayItemWantsCertifiedImage(item) {
			if len(registry[entitycatalog.NormalizeVerifiedAssetKey(overlayItemEntityName(item))]) > 0 {
				return true
			}
			continue
		}
		for _, segment := range chainSegments(item) {
			if !chainEntitySegment(segment) {
				continue
			}
			if len(registry[entitycatalog.NormalizeVerifiedAssetKey(chainSegmentName(segment))]) > 0 {
				return true
			}
		}
	}
	return false
}

func bindVerifiedChainAssets(item capmodel.OverlayItem, registry map[string][]capmodel.OverlayAssetRef, seen map[string]int) capmodel.OverlayItem {
	segments := chainSegments(item)
	if len(segments) == 0 {
		return item
	}
	bound := false
	for _, segment := range segments {
		if !chainEntitySegment(segment) {
			continue
		}
		if assets, _ := segment["assets"].([]any); len(assets) > 0 {
			continue
		}
		key := entitycatalog.NormalizeVerifiedAssetKey(chainSegmentName(segment))
		refs := registry[key]
		if len(refs) == 0 {
			continue
		}
		ref := refs[seen[key]%len(refs)]
		seen[key]++
		setChainSegmentAsset(segment, ref)
		item.AssetRefs = appendUniqueAssetRef(item.AssetRefs, ref)
		bound = true
	}
	if bound {
		// segments are references into Params["chain_segments"]; mutating the
		// maps is enough. Replacing the slice here would change its Go type and
		// break the chain contract/fingerprint.
		item.RenderKey = ""
	}
	return item
}

// chainEntitySegment limits registry binding to segments that can actually
// render an image. A phrase segment must never acquire a portrait just because
// its text happens to match an entity registry key.
func chainEntitySegment(segment map[string]any) bool {
	kind, _ := segment["kind"].(string)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "entity_image", "entity_card", "location", "organization", "concept":
		return true
	case "image":
		caption, _ := segment["entity_caption"].(string)
		return strings.TrimSpace(caption) != ""
	default:
		return false
	}
}

func chainSegmentName(segment map[string]any) string {
	if caption, _ := segment["entity_caption"].(string); strings.TrimSpace(caption) != "" {
		return caption
	}
	text, _ := segment["text"].(string)
	return text
}

func appendUniqueAssetRef(refs []capmodel.OverlayAssetRef, ref capmodel.OverlayAssetRef) []capmodel.OverlayAssetRef {
	for _, existing := range refs {
		if existing.AssetID == ref.AssetID {
			return refs
		}
	}
	return append(refs, ref)
}

func chainSegments(item capmodel.OverlayItem) []map[string]any {
	if item.Params == nil {
		return nil
	}
	raw, ok := item.Params["chain_segments"]
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0)
	switch segments := raw.(type) {
	case []any:
		for _, entry := range segments {
			if segment, ok := entry.(map[string]any); ok {
				out = append(out, segment)
			}
		}
	case []map[string]any:
		out = append(out, segments...)
	}
	return out
}

func setChainSegmentAsset(segment map[string]any, ref capmodel.OverlayAssetRef) {
	if preset, _ := segment["image_preset_id"].(string); strings.TrimSpace(preset) == "" {
		segment["image_preset_id"] = "image_fade_in"
	}
	segment["asset_id"] = ref.AssetID
	segment["sha256"] = ref.SHA256
	segment["media_type"] = ref.MediaType
	segment["url"] = ref.URL
	segment["assets"] = []any{map[string]any{
		"asset_id":   ref.AssetID,
		"sha256":     ref.SHA256,
		"media_type": ref.MediaType,
		"url":        ref.URL,
	}}
}

// verifiedEntityTextCard is the fail-safe for an unregistered identity. It
// keeps the name and timing visible and never renders an unverified picture.
func verifiedEntityTextCard(item capmodel.OverlayItem, name, planID string) capmodel.OverlayItem {
	item.Kind = "organization"
	item.TemplateID = "org_default"
	item.PresetID = "phrase_default"
	item.ImagePresetID = ""
	item.EntityCaption = ""
	item.CaptionMotionID = ""
	item.CaptionMotionParams = nil
	item.CaptionFontFamily = ""
	item.CaptionLayout = ""
	item.AssetRefs = nil
	item.ImageLayers = nil
	item.GroupMotionID = ""
	item.CameraMotionID = ""
	item.EntityStyleID = ""
	item.Frame = nil
	item.Map = nil
	item.Text = name
	item.Params = nil
	item.Style = nil
	item.MotionParams = nil
	item.RenderKey = ""
	// The card used to be an image, so its MotionID belongs to the image
	// target. Always replace it with a text motion from the random pool; an
	// image motion on a text layer is rejected by the renderer.
	item.MotionID = capabilityoverlay.SelectTextMotion(planID, item.SceneID, item.ID)
	return item
}

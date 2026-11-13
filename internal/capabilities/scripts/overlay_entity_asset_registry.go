package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// entityAssetManifest is the reviewed bridge between a named identity and a
// local file whose bytes have already been checked by an operator. A hash in
// the semantic bundle proves that some bytes exist; it does not prove that the
// picture depicts the person or place. This registry is the semantic gate.
type entityAssetManifest struct {
	Version  int                           `json:"version"`
	Entities map[string][]entityAssetEntry `json:"entities"`
}

type entityAssetEntry struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
}

var entityAssetRegistryCandidates = []string{
	"config/entity_asset_registry.json",
	"refactored/config/entity_asset_registry.json",
	"../ops/assets-store/entity-asset-registry.json",
	"ops/assets-store/entity-asset-registry.json",
}

// loadVerifiedEntityAssets resolves the registry from VELOX_ENTITY_ASSET_REGISTRY
// or the deployment-local candidates. Every entry is hashed again before use:
// a stale path or a modified file is skipped instead of being rendered.
func loadVerifiedEntityAssets() map[string][]capmodel.OverlayAssetRef {
	path := strings.TrimSpace(os.Getenv("VELOX_ENTITY_ASSET_REGISTRY"))
	if path == "" {
		for _, candidate := range entityAssetRegistryCandidates {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var manifest entityAssetManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil
	}
	baseDir := filepath.Dir(path)
	registry := make(map[string][]capmodel.OverlayAssetRef, len(manifest.Entities))
	for name, entries := range manifest.Entities {
		key := normalizeEntityAssetKey(name)
		if key == "" {
			continue
		}
		for _, entry := range entries {
			ref, ok := verifyEntityAssetEntry(baseDir, entry)
			if !ok {
				continue
			}
			registry[key] = append(registry[key], ref)
		}
	}
	if len(registry) == 0 {
		return nil
	}
	return registry
}

func verifyEntityAssetEntry(baseDir string, entry entityAssetEntry) (capmodel.OverlayAssetRef, bool) {
	sha := strings.ToLower(strings.TrimSpace(entry.SHA256))
	if len(sha) != 64 || strings.TrimSpace(entry.Path) == "" {
		return capmodel.OverlayAssetRef{}, false
	}
	path := strings.TrimSpace(entry.Path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return capmodel.OverlayAssetRef{}, false
	}
	sum, err := sha256File(path)
	if err != nil || !strings.EqualFold(sum, sha) {
		return capmodel.OverlayAssetRef{}, false
	}
	assetID := strings.TrimSpace(entry.AssetID)
	if assetID == "" {
		assetID = sum
	}
	mediaType := strings.TrimSpace(entry.MediaType)
	if mediaType == "" {
		mediaType = entityAssetMediaType(path)
	}
	return capmodel.OverlayAssetRef{
		AssetID:   assetID,
		SHA256:    sum,
		MediaType: mediaType,
		LocalPath: path,
	}, true
}

func sha256File(path string) (string, error) {
	sum, _, err := digest.SHA256File(path)
	return sum, err
}

func entityAssetMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// normalizeEntityAssetKey makes apostrophes, accents and punctuation
// irrelevant for the identity join: "L’Associated Press" and
// "l associated press" must address the same registry row.
func normalizeEntityAssetKey(value string) string {
	value = strings.NewReplacer("’", "'", "‘", "'", "`", "'").Replace(strings.ToLower(strings.TrimSpace(value)))
	var out strings.Builder
	space := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
			space = false
			continue
		}
		if !space && out.Len() > 0 {
			out.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
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
func applyVerifiedEntityAssets(items []capmodel.OverlayItem, planID string) []capmodel.OverlayItem {
	registry := loadVerifiedEntityAssets()
	if len(registry) == 0 || len(items) == 0 {
		return items
	}
	if !planUsesRegisteredEntity(items, registry) {
		return items
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
			key := normalizeEntityAssetKey(name)
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
		case "image_chain":
			out = append(out, bindVerifiedChainAssets(item, registry, seen))
		default:
			out = append(out, item)
		}
	}
	return out
}

func planUsesRegisteredEntity(items []capmodel.OverlayItem, registry map[string][]capmodel.OverlayAssetRef) bool {
	for _, item := range items {
		if overlayItemWantsCertifiedImage(item) {
			if len(registry[normalizeEntityAssetKey(overlayItemEntityName(item))]) > 0 {
				return true
			}
			continue
		}
		if strings.TrimSpace(item.Kind) == "image_chain" {
			for _, segment := range chainSegments(item) {
				caption, _ := segment["entity_caption"].(string)
				if len(registry[normalizeEntityAssetKey(caption)]) > 0 {
					return true
				}
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
	for index, segment := range segments {
		caption, _ := segment["entity_caption"].(string)
		key := normalizeEntityAssetKey(caption)
		refs := registry[key]
		if len(refs) == 0 {
			continue
		}
		ref := refs[seen[key]%len(refs)]
		seen[key]++
		setChainSegmentAsset(segment, ref)
		if index < len(item.AssetRefs) {
			item.AssetRefs[index] = ref
		}
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
	segment["asset_id"] = ref.AssetID
	segment["sha256"] = ref.SHA256
	segment["media_type"] = ref.MediaType
	segment["url"] = ""
	segment["assets"] = []any{map[string]any{
		"asset_id":   ref.AssetID,
		"sha256":     ref.SHA256,
		"media_type": ref.MediaType,
		"url":        "",
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

package scriptgeneration

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	randomOverlaySFXDurationMS = int64(250)
	randomOverlaySFXGainDB     = -18.0
)

type randomOverlaySFXManifest struct {
	Entries []struct {
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
		MediaType string `json:"media_type"`
	} `json:"entries"`
}

type randomOverlaySFXAsset struct {
	path   string
	sha256 string
}

// loadRandomOverlaySFXAssets reads only the checked manifest and verifies each
// selected byte source against its declared digest. It does not mutate catalogs
// or contact Drive. The repository-local SFX cache remains an input-only source.
func loadRandomOverlaySFXAssets() ([]randomOverlaySFXAsset, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("locate random overlay SFX manifest: %w", err)
	}
	manifestPath := ""
	for dir := cwd; ; dir = filepath.Dir(dir) {
		for _, candidate := range []string{
			filepath.Join(dir, "data", "media", "sfx_clip_random", "manifest.json"),
			filepath.Join(dir, "refactored", "data", "media", "sfx_clip_random", "manifest.json"),
		} {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				manifestPath, err = filepath.Abs(candidate)
				if err != nil {
					return nil, fmt.Errorf("resolve random overlay SFX manifest: %w", err)
				}
				break
			}
		}
		if manifestPath != "" || filepath.Dir(dir) == dir {
			break
		}
	}
	if manifestPath == "" {
		return nil, fmt.Errorf("random overlay SFX manifest not found in repository data directory")
	}
	return loadRandomOverlaySFXAssetsFromManifest(manifestPath)
}

func loadRandomOverlaySFXAssetsFromManifest(manifestPath string) ([]randomOverlaySFXAsset, error) {
	manifestPath, err := filepath.Abs(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("resolve random overlay SFX manifest: %w", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read random overlay SFX manifest: %w", err)
	}
	var manifest randomOverlaySFXManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode random overlay SFX manifest: %w", err)
	}
	assets := make([]randomOverlaySFXAsset, 0, len(manifest.Entries))
	for i, entry := range manifest.Entries {
		if !strings.EqualFold(strings.TrimSpace(entry.MediaType), "audio/mp4") || len(entry.SHA256) != digest.SHA256Size*2 || strings.Trim(entry.SHA256, "0123456789abcdefABCDEF") != "" {
			return nil, fmt.Errorf("random overlay SFX manifest entry %d has invalid media_type or sha256", i)
		}
		const manifestDir = "data/media/sfx_clip_random/"
		entryPath := filepath.ToSlash(filepath.FromSlash(entry.Path))
		if !strings.HasPrefix(entryPath, manifestDir) {
			return nil, fmt.Errorf("random overlay SFX manifest entry %d path must be rooted under %q", i, manifestDir)
		}
		relativePath := strings.TrimPrefix(entryPath, manifestDir)
		if relativePath == "" || filepath.Base(relativePath) != relativePath || filepath.IsAbs(relativePath) || filepath.Clean(relativePath) != relativePath || relativePath == "." || relativePath == ".." {
			return nil, fmt.Errorf("random overlay SFX manifest entry %d has unsafe path %q", i, entry.Path)
		}
		path := filepath.Join(filepath.Dir(manifestPath), relativePath)
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read random overlay SFX %q: %w", entry.Path, err)
		}
		sum := digest.SHA256Sum(contents)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), entry.SHA256) {
			return nil, fmt.Errorf("random overlay SFX %q SHA-256 does not match manifest", entry.Path)
		}
		assets = append(assets, randomOverlaySFXAsset{path: path, sha256: strings.ToLower(entry.SHA256)})
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("random overlay SFX manifest has no entries")
	}
	return assets, nil
}

// attachRandomOverlaySFX selects cues from stable plan/item/content identity.
// The duration is capped by both the cue's certified minimum source length
// (250ms; all bundle assets were probed >=395ms) and the visible image window.
func attachRandomOverlaySFX(plans []*capoverlay.OverlayPlan) error {
	needsAssets := false
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		for _, item := range plan.Items {
			if item.SoundEffect == nil && isImageOverlaySemanticItem(item) {
				needsAssets = true
				break
			}
		}
	}
	if !needsAssets {
		return nil
	}
	assets, err := loadRandomOverlaySFXAssets()
	if err != nil {
		return err
	}
	return attachRandomOverlaySFXFromAssets(plans, assets)
}

func attachRandomOverlaySFXFromAssets(plans []*capoverlay.OverlayPlan, assets []randomOverlaySFXAsset) error {
	if len(assets) == 0 {
		return fmt.Errorf("random overlay SFX asset set is empty")
	}
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		changed := false
		for i := range plan.Items {
			item := &plan.Items[i]
			if item.SoundEffect != nil || !isImageOverlaySemanticItem(*item) {
				continue
			}
			startOffsetMS := int64(0)
			visibleEndMS := item.EndMs - item.StartMs
			assetHashes := make([]string, 0, len(item.AssetRefs))
			for _, ref := range item.AssetRefs {
				assetHashes = append(assetHashes, strings.ToLower(strings.TrimSpace(ref.SHA256)))
			}
			if len(item.ImageLayers) > 0 {
				startOffsetMS = item.ImageLayers[0].StartMS
				visibleEndMS = item.ImageLayers[0].EndMS
				for _, layer := range item.ImageLayers[1:] {
					if layer.StartMS < startOffsetMS {
						startOffsetMS = layer.StartMS
						visibleEndMS = layer.EndMS
					} else if layer.StartMS == startOffsetMS && layer.EndMS > visibleEndMS {
						visibleEndMS = layer.EndMS
					}
				}
				if visibleEndMS > item.EndMs-item.StartMs {
					visibleEndMS = item.EndMs - item.StartMs
				}
			}
			visibleDurationMS := visibleEndMS - startOffsetMS
			if visibleDurationMS <= 0 {
				return fmt.Errorf("overlay item %q has no positive visible image window for SFX", item.ID)
			}
			durationMS := randomOverlaySFXDurationMS
			if durationMS > visibleDurationMS {
				durationMS = visibleDurationMS
			}
			if len(assetHashes) == 0 {
				return fmt.Errorf("overlay item %q has no content-addressed image asset for SFX selection", item.ID)
			}
			sort.Strings(assetHashes)
			identity := fmt.Sprintf("%d:%s%d:%s%d:%s", len(plan.PlanID), plan.PlanID, len(item.ID), item.ID, len(strings.Join(assetHashes, ",")), strings.Join(assetHashes, ","))
			seed := digest.SHA256Sum([]byte(identity))
			asset := assets[int(seed[0])%len(assets)]
			assetID := "overlay-sfx:" + asset.sha256
			logicalPath := "assets/semantic/overlay-sfx-" + asset.sha256 + ".m4a"
			localPath, err := filepath.Abs(asset.path)
			if err != nil {
				return fmt.Errorf("resolve selected random overlay SFX path for item %q: %w", item.ID, err)
			}
			if strings.TrimSpace(item.Kind) == "" && isImageOverlayTemplate(item.TemplateID) {
				item.Kind = "image"
			}
			item.SoundEffect = &capoverlay.OverlaySoundEffect{
				AssetRef: capoverlay.OverlayAssetRef{
					AssetID: assetID, URL: logicalPath, LocalPath: localPath,
					SHA256: asset.sha256, MediaType: "audio/mp4",
				},
				StartOffsetMS: startOffsetMS,
				DurationMS:    durationMS,
				GainDB:        randomOverlaySFXGainDB,
			}
			item.RenderKey = ""
			changed = true
		}
		if changed {
			plan.Fingerprint = ""
			if err := plan.Validate(); err != nil {
				return fmt.Errorf("seal overlay SFX plan %q: %w", plan.PlanID, err)
			}
		}
	}
	return nil
}

func isImageOverlaySemanticItem(item capoverlay.OverlayItem) bool {
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	return (kind == "image" || kind == "entity_image" || kind == "image_popup" || kind == "product" || kind == "logo" ||
		kind == "" && isImageOverlayTemplate(item.TemplateID)) && (len(item.AssetRefs) > 0 || len(item.ImageLayers) > 0)
}

func isImageOverlayTemplate(templateID string) bool {
	switch strings.ToUpper(strings.TrimSpace(templateID)) {
	case "IMAGE_OVERLAY", "IMAGE_POPUP", "ENTITY_IMAGE", "PRODUCT", "LOGO":
		return true
	default:
		return false
	}
}

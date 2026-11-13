// Package scriptgeneration — entity_bundle_media.go owns the bridge from the
// verified SemanticRenderBundle to the overlay planner's entity media index.
//
// The bundle is the single source of truth for entity image bytes. The planner
// must never rebuild that truth from annotations: annotations are the producer
// surface and may be absent, localized or stale by the time a plan is lowered.
// This catalog indexes bundle assets under their canonical identity and keeps
// the annotation surface only as a localized alias source.
package scriptgeneration

import (
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type entityBundleCatalogEntry struct {
	asset        capabilityoverlay.BoundAsset
	stable       string
	canonical    string
	sceneID      string
	occurrenceID string
}

type entityBundleCatalog struct {
	entries      []entityBundleCatalogEntry
	byAssetID    map[string]int
	byHash       map[string]int
	byStable     map[string]int
	byOccurrence map[string]int
	entityByID   map[string]capabilityoverlay.ResolvedEntity
}

func newEntityBundleCatalog(bundle *capabilityoverlay.SemanticRenderBundleV1) *entityBundleCatalog {
	catalog := &entityBundleCatalog{
		byAssetID:    make(map[string]int),
		byHash:       make(map[string]int),
		byStable:     make(map[string]int),
		byOccurrence: make(map[string]int),
		entityByID:   make(map[string]capabilityoverlay.ResolvedEntity),
	}
	if bundle == nil {
		return catalog
	}
	entityByOccurrence := make(map[string]capabilityoverlay.ResolvedEntity, len(bundle.Entities))
	for _, entity := range bundle.Entities {
		if strings.TrimSpace(entity.EntityID) == "" {
			continue
		}
		catalog.entityByID[entity.EntityID] = entity
		if strings.TrimSpace(entity.OccurrenceID) != "" {
			entityByOccurrence[entity.OccurrenceID] = entity
		}
	}
	for _, asset := range bundle.Assets {
		if !asset.Verified || strings.TrimSpace(asset.AssetID) == "" || strings.TrimSpace(asset.ContentHash) == "" {
			continue
		}
		if bundleAssetURL(asset) == "" {
			continue
		}
		entity, ok := entityByOccurrence[asset.OccurrenceID]
		if !ok {
			entity = catalog.entityByID[asset.EntityID]
		}
		canonical := strings.TrimSpace(entity.CanonicalEntityID)
		if canonical == "" {
			canonical = strings.TrimSpace(asset.EntityID)
		}
		occurrenceID := strings.TrimSpace(asset.OccurrenceID)
		sceneID := strings.TrimSpace(entity.SceneID)
		if sceneID == "" && occurrenceID != "" {
			sceneID = strings.TrimSpace(strings.SplitN(occurrenceID, "::", 2)[0])
		}
		entry := entityBundleCatalogEntry{
			asset: asset, stable: strings.TrimSpace(asset.EntityID),
			canonical: canonical, sceneID: sceneID, occurrenceID: occurrenceID,
		}
		index := len(catalog.entries)
		catalog.entries = append(catalog.entries, entry)
		catalog.byAssetID[entry.asset.AssetID] = index
		catalog.byHash[strings.ToLower(entry.asset.ContentHash)] = index
		if _, exists := catalog.byStable[entry.stable]; !exists {
			catalog.byStable[entry.stable] = index
		}
		if occurrenceID != "" {
			catalog.byOccurrence[occurrenceID] = index
		}
	}
	return catalog
}

func (c *entityBundleCatalog) enabled() bool {
	return c != nil && len(c.entries) > 0
}

// assetFor resolves an annotation binding to the bundle asset that owns the
// same bytes. A binding whose asset is absent from the bundle is deliberately
// not resolved: the bundle is authoritative once it carries assets.
func (c *entityBundleCatalog) assetFor(stable string, binding *scriptpkg.EntityImageBinding) (entityBundleCatalogEntry, bool) {
	if c == nil {
		return entityBundleCatalogEntry{}, false
	}
	if binding != nil {
		assetID := strings.TrimSpace(binding.AssetID)
		hash := strings.ToLower(strings.TrimSpace(binding.SHA256))
		if assetID != "" || hash != "" {
			if index, ok := c.byAssetID[assetID]; ok {
				return c.entries[index], true
			}
			if index, ok := c.byHash[hash]; ok {
				return c.entries[index], true
			}
			return entityBundleCatalogEntry{}, false
		}
	}
	if index, ok := c.byStable[strings.TrimSpace(stable)]; ok {
		return c.entries[index], true
	}
	return entityBundleCatalogEntry{}, false
}

func (c *entityBundleCatalog) canonicalFor(stable string) string {
	if c == nil {
		return ""
	}
	if index, ok := c.byStable[strings.TrimSpace(stable)]; ok {
		return c.entries[index].canonical
	}
	if entity, ok := c.entityByID[strings.TrimSpace(stable)]; ok {
		return strings.TrimSpace(entity.CanonicalEntityID)
	}
	return ""
}

func (entry entityBundleCatalogEntry) lookupID(sceneScoped bool) string {
	if sceneScoped && strings.TrimSpace(entry.sceneID) != "" {
		return strings.TrimSpace(entry.sceneID) + "::" + entry.canonical
	}
	return entry.canonical
}

func (c *entityBundleCatalog) indexAsset(index *capabilityentities.EntityMediaIndex, lookupID string, entry entityBundleCatalogEntry, localPathByAsset map[string]string, score float64) error {
	if index == nil || strings.TrimSpace(lookupID) == "" {
		return nil
	}
	localPath := strings.TrimSpace(entry.asset.LocalPath)
	if !usableLocalAssetPath(localPath) {
		localPath = localPathByAsset[entry.asset.AssetID]
	}
	if score <= 0 {
		score = 0.9
	}
	return index.IndexForCanonicalID(lookupID, capabilityentities.EntityAsset{
		AssetID: entry.asset.AssetID, AssetType: bundleAssetType(entry.asset),
		SHA256: entry.asset.ContentHash, StorageURL: bundleAssetURL(entry.asset),
		LocalPath: localPath, QualityScore: score, Source: "semantic_render_bundle",
	})
}

func bundleAssetURL(asset capabilityoverlay.BoundAsset) string {
	url := strings.TrimSpace(asset.SourceURL)
	if url == "" && strings.TrimSpace(asset.DriveFileID) != "" {
		url = "https://drive.google.com/uc?export=download&id=" + strings.TrimSpace(asset.DriveFileID)
	}
	return url
}

func bundleAssetType(asset capabilityoverlay.BoundAsset) string {
	url := strings.ToLower(bundleAssetURL(asset))
	switch {
	case strings.HasSuffix(url, ".png"):
		return "photo"
	case strings.HasSuffix(url, ".jpg"), strings.HasSuffix(url, ".jpeg"):
		return "photo_jpeg"
	default:
		return "photo_jpeg"
	}
}

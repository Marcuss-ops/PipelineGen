package overlays

import (
	"context"
	"fmt"
)

// AssetPreparer is the canonical owner of overlay asset warming. Both
// speculative queue prefetch and overlay.prepare use this same implementation.
type AssetPreparer struct {
	cache *Cache
}

func NewAssetPreparer(cache *Cache) (*AssetPreparer, error) {
	if cache == nil {
		return nil, fmt.Errorf("overlay asset preparer: cache is required")
	}
	return &AssetPreparer{cache: cache}, nil
}

// Prepare materializes each unique content-addressed asset and returns the
// resulting local paths. It does not mutate queue/job state.
func (p *AssetPreparer) Prepare(ctx context.Context, assets []AssetRef) ([]string, error) {
	if p == nil || p.cache == nil {
		return nil, fmt.Errorf("overlay asset preparer is not wired")
	}
	paths := make([]string, 0, len(assets))
	seen := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		if _, ok := seen[asset.SHA256]; ok {
			continue
		}
		seen[asset.SHA256] = struct{}{}
		path, err := p.cache.EnsureAsset(ctx, asset.URL, asset.SHA256)
		if err != nil {
			return nil, fmt.Errorf("prepare asset %s: %w", asset.AssetID, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

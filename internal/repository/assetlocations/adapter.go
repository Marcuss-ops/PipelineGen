package assetlocations

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// Adapter implements asset.LocationRepository by wrapping the concrete *Repository
// and performing necessary conversions between domain entities and repository types.
type Adapter struct {
	inner *Repository
}

// NewAdapter wraps a concrete *Repository as an asset.LocationRepository.
func NewAdapter(inner *Repository) *Adapter {
	return &Adapter{inner: inner}
}

func toDomain(loc *AssetLocation) *asset.Location {
	if loc == nil {
		return nil
	}
	return &asset.Location{
		ID:            loc.ID,
		AssetID:       loc.AssetID,
		LocationKind:  asset.LocationKind(loc.LocationKind),
		URI:           loc.URI,
		ExternalID:    loc.ExternalID,
		AccessURL:     loc.WebViewLink,
		DownloadURL:   loc.DownloadURL,
		MimeType:      loc.MimeType,
		FileSizeBytes: loc.FileSizeBytes,
		FileHash:      loc.FileHash,
		IsPrimary:     loc.IsPrimary,
		CreatedAt:     loc.CreatedAt,
		UpdatedAt:     loc.UpdatedAt,
	}
}

func toRepo(loc *asset.Location) AssetLocation {
	if loc == nil {
		return AssetLocation{}
	}
	return AssetLocation{
		ID:            loc.ID,
		AssetID:       loc.AssetID,
		LocationKind:  LocationKind(loc.LocationKind),
		URI:           loc.URI,
		ExternalID:    loc.ExternalID,
		WebViewLink:   loc.AccessURL,
		DownloadURL:   loc.DownloadURL,
		MimeType:      loc.MimeType,
		FileSizeBytes: loc.FileSizeBytes,
		FileHash:      loc.FileHash,
		IsPrimary:     loc.IsPrimary,
		CreatedAt:     loc.CreatedAt,
		UpdatedAt:     loc.UpdatedAt,
	}
}

func (a *Adapter) Upsert(ctx context.Context, loc *asset.Location) error {
	return a.inner.Upsert(ctx, toRepo(loc))
}

func (a *Adapter) GetPrimary(ctx context.Context, assetID string) (*asset.Location, error) {
	loc, err := a.inner.GetPrimary(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return toDomain(loc), nil
}

func (a *Adapter) ListByAsset(ctx context.Context, assetID string) ([]*asset.Location, error) {
	locs, err := a.inner.GetByAssetID(ctx, assetID)
	if err != nil {
		return nil, err
	}
	res := make([]*asset.Location, len(locs))
	for i := range locs {
		res[i] = toDomain(&locs[i])
	}
	return res, nil
}

func (a *Adapter) SetPrimary(ctx context.Context, assetID string, kind asset.LocationKind) error {
	return a.inner.SetPrimary(ctx, assetID, LocationKind(kind))
}

func (a *Adapter) Delete(ctx context.Context, assetID string, kind asset.LocationKind) error {
	return a.inner.Delete(ctx, assetID, LocationKind(kind))
}

func (a *Adapter) DeleteAll(ctx context.Context, assetID string) error {
	return a.inner.DeleteAll(ctx, assetID)
}

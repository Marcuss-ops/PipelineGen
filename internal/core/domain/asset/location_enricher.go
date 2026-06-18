// Package asset — location_enricher.go provides helpers to hydrate
// the deprecated location fields (DriveFileID, DriveLink, LocalPath,
// etc.) on an asset.MediaAsset from the canonical asset_locations table.
//
// This is a transitional adapter: once all consumers read locations
// directly via LocationRepository, the deprecated fields and this
// file can be removed.
package asset

import "context"

// LocationEnricher hydrates deprecated location fields on MediaAsset
// from the canonical LocationRepository. It is meant to be called
// after reading from media_assets (which no longer has the source-of-truth
// for locations once the migration is complete).
type LocationEnricher struct {
	locRepo LocationRepository
}

// NewLocationEnricher creates an enricher backed by locRepo.
func NewLocationEnricher(locRepo LocationRepository) *LocationEnricher {
	return &LocationEnricher{locRepo: locRepo}
}

// Enrich hydrates the deprecated fields on m from asset_locations.
// It is a no-op when locRepo is nil or m is nil/has no ID.
// The fields populated are:
//
//	LocalPath   ← primary "local" location URI
//	DriveFileID ← "drive" location ExternalID
//	DriveLink   ← "drive" location AccessURL
//	DownloadLink← "drive" location DownloadURL
//	FileHash    ← primary location FileHash
func (e *LocationEnricher) Enrich(ctx context.Context, m *MediaAsset) {
	if e == nil || e.locRepo == nil || m == nil || m.ID == "" {
		return
	}

	locs, err := e.locRepo.ListByAsset(ctx, m.ID)
	if err != nil || len(locs) == 0 {
		return
	}

	for _, loc := range locs {
		switch loc.LocationKind {
		case LocationKindLocal:
			if m.LocalPath == "" {
				m.LocalPath = loc.URI
			}
			if m.FileHash == "" {
				m.FileHash = loc.FileHash
			}
		case LocationKindDrive:
			if m.DriveFileID == "" {
				m.DriveFileID = loc.ExternalID
			}
			if m.DriveLink == "" {
				m.DriveLink = loc.AccessURL
			}
			if m.DownloadLink == "" {
				m.DownloadLink = loc.DownloadURL
			}
			if m.FileHash == "" {
				m.FileHash = loc.FileHash
			}
		}
	}
}

// EnrichSlice enriches a slice of MediaAsset pointers in-place.
func (e *LocationEnricher) EnrichSlice(ctx context.Context, items []*MediaAsset) {
	if e == nil || len(items) == 0 {
		return
	}
	for _, m := range items {
		e.Enrich(ctx, m)
	}
}

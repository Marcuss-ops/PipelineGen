// Package assetquery provides an aggregated, read-only view of media assets
// joined with their physical locations, processing records, and current
// version.
//
// Details is the canonical replacement for the read paths that historically
// pulled fields off models.MediaAsset (LocalPath, DriveLink, Status, etc.).
// New code MUST obtain Details via Service.Get and read from there; it MUST
// NOT import internal/media/models, MUST NOT call LocationEnricher directly,
// and MUST NOT add fields back onto assets.Asset.
package assetquery

import (
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// Details is the aggregated read-only view of a single media asset.
//
// Consumers must treat the slices as read-only; mutating them would
// silently diverge from the canonical repositories.
type Details struct {
	// Asset is the canonical media_asset row.
	//
	// The deprecated fields on assets.Asset (LocalPath, DriveLink,
	// DriveFileID, DownloadLink, FileHash, Status, Error) MUST be
	// considered transitional: source-of-truth lives in Locations and
	// Processing below. They are still populated today because
	// assetrepo.Repository.Get invokes LocationEnricher — this filler
	// is removed in PR2 (codex/asset-location-exclusive). New consumers
	// MUST read from Details.LocalLocation / DriveLocation /
	// ProcessingStep instead, even if the deprecated fields look
	// non-empty, so that PR2 is transparent at the call site.
	Asset *assets.Asset

	// Locations is every asset_locations row for the asset. Primary
	// locations are first.
	Locations []*assets.Location

	// Processing is every asset_processing row for the asset.
	Processing []assets.ProcessingRecord

	// CurrentVersion is the latest asset_versions row, or nil when the
	// asset has no versions yet (asset_versions table is still
	// scheduled for a follow-up migration).
	CurrentVersion *assets.Version
}

// LocalLocation returns the local Location for the asset.
//
// Priority:
//  1. Primary local Location (is_primary=1 and location_kind='local')
//  2. First non-primary local Location
//  3. nil when no local Location exists
//
// Returns nil when d is nil or Locations is empty.
func (d *Details) LocalLocation() *assets.Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == assets.LocationKindLocal {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == assets.LocationKindLocal {
			return loc
		}
	}
	return nil
}

// DriveLocation returns the drive Location for the asset.
//
// Priority:
//  1. Primary drive Location (is_primary=1 and location_kind='drive')
//  2. First non-primary drive Location
//  3. nil when no drive Location exists
//
// Returns nil when d is nil or Locations is empty.
func (d *Details) DriveLocation() *assets.Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == assets.LocationKindDrive {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == assets.LocationKindDrive {
			return loc
		}
	}
	return nil
}

// ProcessingStep returns a pointer to the processing record for the given
// step name, or nil if no such record exists. The pointer points into
// d.Processing; callers MUST NOT mutate the underlying struct.
//
// step match is exact (case-sensitive). Use the canonical
// asset.StageDownload / StageIndexing / ... constants so step names stay
// aligned with the domain.
func (d *Details) ProcessingStep(step string) *assets.ProcessingRecord {
	if d == nil {
		return nil
	}
	for i := range d.Processing {
		if d.Processing[i].Step == step {
			return &d.Processing[i]
		}
	}
	return nil
}

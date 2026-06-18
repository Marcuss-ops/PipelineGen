// Package assetquery provides an aggregated, read-only view of media assets
// joined with their physical locations, processing records, and current
// version.
package assetquery

import "github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"

// Details is the canonical read model for a media asset. The logical entity,
// physical locations, processing records, and current version remain separate
// so no consumer needs a legacy all-in-one struct.
type Details struct {
	Asset          *asset.MediaAsset
	Locations      []*asset.Location
	Processing     []asset.ProcessingRecord
	CurrentVersion *asset.Version
}

// LocalLocation returns the preferred local location.
func (d *Details) LocalLocation() *asset.Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == asset.LocationKindLocal && loc.IsPrimary {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == asset.LocationKindLocal {
			return loc
		}
	}
	return nil
}

// DriveLocation returns the preferred Drive location.
func (d *Details) DriveLocation() *asset.Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == asset.LocationKindDrive && loc.IsPrimary {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == asset.LocationKindDrive {
			return loc
		}
	}
	return nil
}

// ProcessingStep returns the record for a canonical processing step.
func (d *Details) ProcessingStep(step string) *asset.ProcessingRecord {
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

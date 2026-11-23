package asset

// Details is the full representation of an asset including all sub-entities.
type Details struct {
	Asset      *Asset              `json:"asset"`
	Locations  []*Location         `json:"locations,omitempty"`
	Processing []*ProcessingRecord `json:"processing,omitempty"`
	Versions   []*Version          `json:"versions,omitempty"`
}

// LocalLocation returns the local Location for the asset.
func (d *Details) LocalLocation() *Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == LocationKindLocal {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == LocationKindLocal {
			return loc
		}
	}
	return nil
}

// ObjectStorageLocation returns the object-storage Location for the asset.
// It prefers the primary row, then any object-storage row: an asset delivered
// without Drive (CR-19) has this as its only durable location.
func (d *Details) ObjectStorageLocation() *Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == LocationKindObjectStorage {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == LocationKindObjectStorage {
			return loc
		}
	}
	return nil
}

// DriveLocation returns the drive Location for the asset.
func (d *Details) DriveLocation() *Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == LocationKindDrive {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == LocationKindDrive {
			return loc
		}
	}
	return nil
}

// ProcessingStep returns a pointer to the processing record for the given step name.
func (d *Details) ProcessingStep(step string) *ProcessingRecord {
	if d == nil {
		return nil
	}
	for _, p := range d.Processing {
		if p != nil && p.Step == step {
			return p
		}
	}
	return nil
}

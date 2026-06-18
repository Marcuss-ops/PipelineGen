// Package assetquery — Resolver picks the best Location for a media asset
// given a preference ordering.
//
// This is a focused, in-package helper for the assetquery package itself.
// A shared, cross-package LocationResolver interface is expected to land
// in PR4 (codex/migrate-media-consumers). Until then, Resolver lives here
// so clipresolver-style fan-out can be replaced incrementally.
package assetquery

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// ErrNoReadableLocation is returned when ResolveReadable cannot find any
// usable Location matching the preferences and no fallback applies. It is
// distinct from asset.ErrNotFound: the asset exists but has no usable
// storage location.
var ErrNoReadableLocation = errors.New("assetquery: no readable location found")

// Resolver picks the most appropriate Location for a media asset given an
// ordered preference list. It encapsulates the in-line "use local if
// present, else fall back to Drive" logic that today is duplicated across
// clipresolver, ontology, and image pipelines.
type Resolver struct {
	locations asset.LocationRepository
}

// NewResolver returns a Resolver backed by locRepo.
func NewResolver(locRepo asset.LocationRepository) *Resolver {
	return &Resolver{locations: locRepo}
}

// ResolveReadable returns the best Location for assetID honouring the
// ordered preference list.
//
// Algorithm:
//  1. For each preference kind in order:
//     a. Return the primary location of that kind (is_primary=1) if present.
//     b. Otherwise return the first non-primary location of that kind.
//  2. If no preference matches, fall back to the primary location
//     regardless of kind.
//  3. If still nothing matches, return the first non-nil Location.
//  4. Return ErrNoReadableLocation only when the asset has no locations
//     at all.
//
// Errors:
//   - asset.ErrInvalidID if assetID is empty
//   - ErrNoReadableLocation when the asset has zero locations
//   - any transport error returned by the underlying LocationRepository
func (r *Resolver) ResolveReadable(
	ctx context.Context,
	assetID string,
	preferences []asset.LocationKind,
) (*asset.Location, error) {
	if r == nil || r.locations == nil {
		return nil, fmt.Errorf("assetquery.Resolver: missing dependency (nil locRepo)")
	}
	if assetID == "" {
		return nil, asset.ErrInvalidID
	}

	locs, err := r.locations.ListByAsset(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("assetquery.ResolveReadable(%s) list: %w", assetID, err)
	}
	if len(locs) == 0 {
		return nil, ErrNoReadableLocation
	}

	// Honour preferences in order.
	for _, pref := range preferences {
		if loc := pickKind(locs, pref, true); loc != nil {
			return loc, nil
		}
		if loc := pickKind(locs, pref, false); loc != nil {
			return loc, nil
		}
	}

	// Fallback: primary regardless of kind, then first non-nil.
	if loc := primaryLocation(locs); loc != nil {
		return loc, nil
	}
	for _, loc := range locs {
		if loc != nil {
			return loc, nil
		}
	}
	// Defensive: listByAsset returned rows but all are nil — treat as no locations.
	return nil, ErrNoReadableLocation
}

// pickKind returns the first Location of the requested kind. When
// preferPrimary is true it short-circuits on the primary; when false it
// returns the first non-nil match so the caller can fall back if the
// preferred kind exists only as non-primary.
func pickKind(locs []*asset.Location, kind asset.LocationKind, preferPrimary bool) *asset.Location {
	var fallback *asset.Location
	for _, loc := range locs {
		if loc == nil || loc.LocationKind != kind {
			continue
		}
		if preferPrimary && loc.IsPrimary {
			return loc
		}
		if !preferPrimary && fallback == nil {
			fallback = loc
		}
	}
	return fallback
}

// primaryLocation returns the first location flagged is_primary=1, or nil.
func primaryLocation(locs []*asset.Location) *asset.Location {
	for _, loc := range locs {
		if loc != nil && loc.IsPrimary {
			return loc
		}
	}
	return nil
}

// Package asset — LocationRepository is the canonical domain contract
// for the asset_locations persistence table. Services MUST depend on
// this interface, NOT on the legacy concrete *assetlocations.Repository.
//
// The interface operates on canonical *Location / LocationKind, mirroring
// the design of asset.Repository. Implementations are responsible for
// transactional outbox emission on each mutation (Upsert, SetPrimary,
// Delete, DeleteAll).
package asset

import "context"

// LocationRepository is the canonical domain contract for asset_locations
// persistence. One asset can have multiple locations (a local file, a
// Drive copy, an S3 mirror) with at most one designated as primary.
//
// All mutating methods (Upsert, SetPrimary, Delete, DeleteAll) write an
// outbox_event in the same database transaction as the data change so
// consumers can subscribe via the canonical pipeline.
type LocationRepository interface {
	// Upsert inserts or replaces a location. The (asset_id, location_kind)
	// unique constraint ensures at most one record per kind per asset.
	// Emits "location.upserted" outbox event in the same transaction.
	Upsert(ctx context.Context, loc *Location) error

	// GetPrimary returns the primary location for an asset (is_primary=1),
	// or nil if the asset has no primary. Read-only.
	GetPrimary(ctx context.Context, assetID string) (*Location, error)

	// ListByAsset returns all location records for an asset, primary
	// first. Read-only.
	ListByAsset(ctx context.Context, assetID string) ([]*Location, error)

	// SetPrimary designates a location as the primary for its asset,
	// unmarking any previous primary. Atomic: there's never a moment
	// where the asset has 0 or 2 primary locations.
	// Emits "location.primary_set" outbox event in the same transaction.
	SetPrimary(ctx context.Context, assetID string, kind LocationKind) error

	// Delete removes a single location record (asset_id, location_kind).
	// Emits "location.deleted" outbox event in the same transaction.
	Delete(ctx context.Context, assetID string, kind LocationKind) error

	// DeleteAll removes all location records for an asset.
	// Emits "location.all_deleted" outbox event in the same transaction.
	DeleteAll(ctx context.Context, assetID string) error
}

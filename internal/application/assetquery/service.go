// Package assetquery — Service aggregates reads across the four canonical
// asset repositories: asset, location, processing, version.
//
// Service is the canonical entry point for read access to a media asset's
// full state. It replaces direct reads of internal/media/models.MediaAsset
// (and its deprecated fields) + ad-hoc fan-out to multiple clips/clips-style
// repositories.
//
// PR-by-PR migration rule: new consumers MUST obtain Details via
// Service.Get, MUST NOT import internal/media/models, and MUST NOT add
// fields back onto asset.MediaAsset.
package assetquery

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// Service provides aggregated reads of media assets with their locations,
// processing records, and current version. It does NOT do write-side
// orchestration (that lives in application/assetservice/ — PR2).
type Service struct {
	assets     asset.Repository
	locations  asset.LocationRepository
	processing asset.ProcessingRepository
	versions   asset.VersionRepository
}

// New returns a Service backed by the four canonical repositories.
//
// All four dependencies are required: nil is treated as a programming
// error and the returned Service will return a wrapped error on every
// Get call rather than panicking, so callers fail loudly rather than
// reading incomplete Details.
func New(
	assets asset.Repository,
	locations asset.LocationRepository,
	processing asset.ProcessingRepository,
	versions asset.VersionRepository,
) *Service {
	return &Service{
		assets:     assets,
		locations:  locations,
		processing: processing,
		versions:   versions,
	}
}

// Get returns the aggregated Details for a single asset by ID.
//
// Errors:
//   - asset.ErrInvalidID if id is empty
//   - asset.ErrNotFound if no row exists
//   - asset.ErrSoftDeleted if the asset is soft-deleted (bubbled up
//     from asset.Repository.Get; consumers decide whether to translate
//     it into a 404 or surface it)
//   - any storage/transport error returned by the underlying repositories,
//     wrapped with a per-step annotation so logs identify which sub-query
//     failed
//
// Details.Asset is non-nil on success. Details.Locations, Processing,
// and CurrentVersion may legitimately be empty / nil when the asset has
// no rows in those tables yet (mid-migration state).
func (s *Service) Get(ctx context.Context, id string) (*Details, error) {
	if id == "" {
		return nil, asset.ErrInvalidID
	}
	if s.assets == nil || s.locations == nil || s.processing == nil || s.versions == nil {
		return nil, fmt.Errorf("assetquery.Service: missing dependency (nil repo pointer)")
	}

	a, err := s.assets.Get(ctx, id)
	if err != nil {
		// Includes asset.ErrSoftDeleted — bubble up unchanged so callers
		// can errors.Is(err, asset.ErrSoftDeleted).
		return nil, fmt.Errorf("assetquery.Get(%s) load asset: %w", id, err)
	}
	if a == nil {
		return nil, asset.ErrNotFound
	}

	locs, err := s.locations.ListByAsset(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("assetquery.Get(%s) load locations: %w", id, err)
	}

	proc, err := s.processing.GetByAssetID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("assetquery.Get(%s) load processing: %w", id, err)
	}

	ver, err := s.versions.GetCurrent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("assetquery.Get(%s) load version: %w", id, err)
	}

	return &Details{
		Asset:          a,
		Locations:      locs,
		Processing:     proc,
		CurrentVersion: ver,
	}, nil
}

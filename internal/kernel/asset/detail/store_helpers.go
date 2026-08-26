// Package asset — store contracts + Service ctor (Wave C / Phase 3 slim, Phase 4 unification).
//
// Phase 3 (Wave C / Blocco 1 asset.Asset SSOT, June 2026): the SQL
// receivers and `database/sql` import were moved to Local infra.
//
// Phase 4 (June 2026): the `localMediaAssetColumns` mirror const,
// the `buildMediaAssetQuery` + `buildClipFolderQuery` helpers, and the
// previously-implicit `clipFolderColumns` legacy const they referenced
// were REMOVED from this domain file. Canonical home:
// `internal/platform/sqlite/assets/clips_repository.go`
// exports `MediaAssetColumns` + `ClipFolderColumns`; the production
// query builders stay in the same-package infra scope
// (`internal/platform/sqlite/assets/store_helpers.go`).
//
// What moved to Local infra (Phase 3):
//
//   - 4 receivers (GetFolderChildren/FindByPHash/MarkUsed/MarkClipsUsed) →
//     internal/platform/sqlite/assets/store_helpers.go
//   - 9 Store-CRD receivers (Get/Save/Delete/List) on the asset store →
//     internal/platform/sqlite/assets/asset_store.go
//     (asset_repository_adapter + summary_query)
//   - the concrete SQLite connection and repository adapters now live
//     entirely in internal/platform/sqlite/assets.
//     The domain package retains only the consumer-facing contracts and
//     the type-switch bridge used by Service.
//
// NO stdlib database/sql import, NO fmt/time/timeutil imports. Phase 3
// acceptance: zero stdlib database/sql and sqlite3 hits in this
// domain package.
package detail

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"go.uber.org/zap"
)

// Repository is the canonical CRUD contract for asset persistence.
// Consumer-owned: each caller declares what it needs.
type Repository interface {
	Upsert(ctx context.Context, asset *asset.Asset) error
	Get(ctx context.Context, id string) (*asset.Asset, error)
	List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error)
	Count(ctx context.Context, filter asset.Filter) (int64, error)
	SoftDelete(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
	FindByExternalRef(ctx context.Context, provider, externalID string) (*asset.Asset, error)
}

// LocationRepository is the contract for asset_locations persistence.
type LocationRepository interface {
	// Upsert inserts or updates a location record and returns its ID.
	// The concrete implementation populates loc.ID when the record is
	// created or updated.
	Upsert(ctx context.Context, loc *asset.Location) error
	GetPrimary(ctx context.Context, assetID string) (*asset.Location, error)
	ListByAsset(ctx context.Context, assetID string) ([]*asset.Location, error)
	SetPrimary(ctx context.Context, assetID string, kind asset.LocationKind) error
	Delete(ctx context.Context, assetID string, kind asset.LocationKind) error
	DeleteAll(ctx context.Context, assetID string) error
}

// ProcessingRepository is the contract for asset_processing persistence.
type ProcessingRepository interface {
	Start(ctx context.Context, assetID, step string) error
	Complete(ctx context.Context, assetID, step string) error
	Fail(ctx context.Context, assetID, step, errMsg string) error
	Transition(ctx context.Context, assetID, step string, from, to asset.ProcessingStatus) error
	Get(ctx context.Context, assetID, step string) (*asset.ProcessingRecord, error)
	GetByAssetID(ctx context.Context, assetID string) ([]asset.ProcessingRecord, error)
	GetFailed(ctx context.Context) ([]asset.ProcessingRecord, error)
	Delete(ctx context.Context, assetID, step string) error
	DeleteAll(ctx context.Context, assetID string) error
}

// VersionRepository is the contract for asset_versions persistence.
type VersionRepository interface {
	GetCurrent(ctx context.Context, assetID string) (*asset.Version, error)
	List(ctx context.Context, assetID string) ([]asset.Version, error)
	Append(ctx context.Context, v *asset.Version) error
}

// Store is the high-level unified CRUD repository for assets (with nested entities).
type Store interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
	List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error)
	Save(ctx context.Context, details *asset.Details) error
	Delete(ctx context.Context, id string) error
}

// ArtifactStore manages metadata for stored artifacts.
type ArtifactStore interface {
	Create(ctx context.Context, a *asset.Artifact) error
	Get(ctx context.Context, id string) (*asset.Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*asset.Artifact, error)
	UpdateStatus(ctx context.Context, id string, status asset.ArtifactStatus, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]*asset.Artifact, error)
}

// DeliveryStore manages delivery records.
type DeliveryStore interface {
	Create(ctx context.Context, d *delivery.Delivery) error
	Get(ctx context.Context, id string) (*delivery.Delivery, error)
	Update(ctx context.Context, d *delivery.Delivery) error
	ListPending(ctx context.Context) ([]*delivery.Delivery, error)
}

// ── Service Class ───────────────────────────────────────────────────

// Service is the high-level facade that wraps a Store implementation
// and exposes Repository accessors that type-switch against the
// unexported assetStoreAdapter interface (declared in store_core.go).
type Service struct {
	store Store
	log   *zap.Logger
}

// NewService creates a new Service wrapping the given Store.
func NewService(store Store, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{store: store, log: log}
}

// Get delegates to the wrapped Store.
func (s *Service) Get(ctx context.Context, id string) (*asset.Details, error) {
	return s.store.Get(ctx, id)
}

// List delegates to the wrapped Store.
func (s *Service) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
	return s.store.List(ctx, filter)
}

// Save delegates to the wrapped Store.
func (s *Service) Save(ctx context.Context, details *asset.Details) error {
	return s.store.Save(ctx, details)
}

// Delete delegates to the wrapped Store.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// ── Soft-delete filter (canonical SSOT, PR 1) ───────────────────────

// SoftDeleteFilter returns the canonical SQL fragment that excludes
// soft-deleted rows from query results.
//
// PR 1 (June 2026, Lifecycle state SSOT): the canonical tombstone is
// UPPERCASE 'DELETED'. Pre-PR1 readers accepted both 'deleted' and
// 'DELETED' (mixed-case writers); post-PR1 history rows are rewritten
// to UPPERCASE by migration 101, so a single equality check is enough.
// Compatibility with the pre-101 lower-case value is dropped because
// no production writer emits it anymore and migration 101 is a hard
// pre-condition for the canonical enum to be SSOT.
//
// Phase 4 unification: the column projections (`mediaAssetColumns`,
// `clipFolderColumns`) and the SQL query builders
// (`buildMediaAssetQuery`, `buildClipFolderQuery`, `clipSearchColumns`)
// that previously mirrored here are REMOVED. The canonical home is
// `internal/platform/sqlite/assets/clips_repository.go`
// (`MediaAssetColumns`, `ClipFolderColumns` — now exported) and
// `internal/platform/sqlite/assets/store_helpers.go`
// (the production query builders). Domain retains the soft-delete
// SSOT only — no SQL primitives, no `database/sql` import.
func SoftDeleteFilter() string {
	return "lifecycle_state != 'DELETED'"
}

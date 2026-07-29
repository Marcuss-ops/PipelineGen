// Package asset — Store interface contracts + AssetStoreSQLite marker
// + Service ctor (Wave C / Phase 3 slim, Phase 4 unification).
//
// Phase 3 (Wave C / Blocco 1 Asset SSOT, June 2026): the SQL
// receivers and `database/sql` import were moved to Local infra.
//
// Phase 4 (June 2026): the `localMediaAssetColumns` mirror const,
// the `buildMediaAssetQuery` + `buildClipFolderQuery` helpers, and the
// previously-implicit `clipFolderColumns` legacy const they referenced
// were REMOVED from this domain file. Canonical home:
// `internal/infrastructure/database/sqlite/assets/clips_repository.go`
// exports `MediaAssetColumns` + `ClipFolderColumns`; the production
// query builders stay in the same-package infra scope
// (`internal/infrastructure/database/sqlite/assets/store_helpers.go`).
//
// What moved to Local infra (Phase 3):
//
//   - 4 receivers (GetFolderChildren/FindByPHash/MarkUsed/MarkClipsUsed) →
//     internal/infrastructure/database/sqlite/assets/store_helpers.go
//   - 9 Store-CRD receivers (Get/Save/Delete/List) on `*AssetStoreSQLite` →
//     internal/infrastructure/database/sqlite/assets/asset_store.go
//     (asset_repository_adapter + summary_query)
//   - `db *sql.DB` field on `*AssetStoreSQLite` (Local infra owns the
//     connection handle via embed; the legacy struct no longer needs
//     the field — its presence was an artefact of the pre-Phase-3
//     moved-from-Local/dual-struct shape)
//
// The 4 factory method receivers (AssetRepository/LocationRepository/
// ProcessingRepository/VersionRepository) STAY on the legacy struct
// so the domain Service type-switches against the unexported
// `assetStoreAdapter` interface (defined in store_core.go) without
// importing Local infra. They return nil in the legacy struct because
// the adapter structs (locationRepositoryAdapter/versionRepositoryAdapter/
// processingRepositoryAdapter/assetRepositoryAdapter) moved to
// Local infra; Local's `*sqassets.AssetStoreSQLite` declares its own
// concrete factory methods that shadow the legacy stubs and produce
// the real adapters.
//
// NO stdlib database/sql import, NO fmt/time/timeutil imports. Phase 3
// acceptance: zero stdlib database/sql and sqlite3 hits in this
// domain package.
package asset

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
	"go.uber.org/zap"
)

// Repository is the canonical CRUD contract for asset persistence.
// Consumer-owned: each caller declares what it needs.
type Repository interface {
	Upsert(ctx context.Context, asset *Asset) error
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter Filter) ([]*Asset, error)
	Count(ctx context.Context, filter Filter) (int64, error)
	SoftDelete(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
	FindByExternalRef(ctx context.Context, provider, externalID string) (*Asset, error)
}

// LocationRepository is the contract for asset_locations persistence.
type LocationRepository interface {
	// Upsert inserts or updates a location record and returns its ID.
	// The concrete implementation populates loc.ID when the record is
	// created or updated.
	Upsert(ctx context.Context, loc *Location) error
	GetPrimary(ctx context.Context, assetID string) (*Location, error)
	ListByAsset(ctx context.Context, assetID string) ([]*Location, error)
	SetPrimary(ctx context.Context, assetID string, kind LocationKind) error
	Delete(ctx context.Context, assetID string, kind LocationKind) error
	DeleteAll(ctx context.Context, assetID string) error
}

// ProcessingRepository is the contract for asset_processing persistence.
type ProcessingRepository interface {
	Start(ctx context.Context, assetID, step string) error
	Complete(ctx context.Context, assetID, step string) error
	Fail(ctx context.Context, assetID, step, errMsg string) error
	Transition(ctx context.Context, assetID, step string, from, to ProcessingStatus) error
	Get(ctx context.Context, assetID, step string) (*ProcessingRecord, error)
	GetByAssetID(ctx context.Context, assetID string) ([]ProcessingRecord, error)
	GetFailed(ctx context.Context) ([]ProcessingRecord, error)
	Delete(ctx context.Context, assetID, step string) error
	DeleteAll(ctx context.Context, assetID string) error
}

// VersionRepository is the contract for asset_versions persistence.
type VersionRepository interface {
	GetCurrent(ctx context.Context, assetID string) (*Version, error)
	List(ctx context.Context, assetID string) ([]Version, error)
	Append(ctx context.Context, v *Version) error
}

// Store is the high-level unified CRUD repository for assets (with nested entities).
type Store interface {
	Get(ctx context.Context, id string) (*Details, error)
	List(ctx context.Context, filter Filter) ([]*Summary, error)
	Save(ctx context.Context, details *Details) error
	Delete(ctx context.Context, id string) error
}

// ArtifactStore manages metadata for stored artifacts.
type ArtifactStore interface {
	Create(ctx context.Context, a *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error)
	UpdateStatus(ctx context.Context, id string, status ArtifactStatus, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]*Artifact, error)
}

// DeliveryStore manages delivery records.
type DeliveryStore interface {
	Create(ctx context.Context, d *delivery.Delivery) error
	Get(ctx context.Context, id string) (*delivery.Delivery, error)
	Update(ctx context.Context, d *delivery.Delivery) error
	ListPending(ctx context.Context) ([]*delivery.Delivery, error)
}

// ── AssetStoreSQLite (legacy marker for HYBRID embed promotion) ────

// AssetStoreSQLite is the SQLite-backed implementation marker kept in
// domain for backwards naming compatibility AND the HYBRID embed
// promotion contract (Local infra embeds this struct to inherit the
// nil-returning factory stubs below; Local's own concrete receivers
// shadow the stubs).
//
// Wave C / Phase 3 (June 2026): the legacy struct no longer owns the
// SQLite connection handle. Local `*sqassets.AssetStoreSQLite` owns
// `db *sql.DB`; the legacy struct's only state is the logger (Local
// shadows it via its own `log *zap.Logger` field on the outer struct).
//
// Ctor signature change: `NewAssetStoreSQLite(log *zap.Logger)` (no
// db parameter) — production callers MUST construct via
// `sqassets.NewAssetStoreSQLite(*sql.DB, *zap.Logger)`. The legacy
// ctor is preserved only for the Local infra embed sites in
// `internal/infrastructure/database/sqlite/assets/{asset_store,clips_repository}.go`.
type AssetStoreSQLite struct {
	log *zap.Logger
}

// NewAssetStoreSQLite creates a new AssetStoreSQLite marker with the
// given logger. Production callers should use the Local ctor
// `sqassets.NewAssetStoreSQLite(*sql.DB, *zap.Logger)` which embeds
// this struct AND owns the db handle.
func NewAssetStoreSQLite(log *zap.Logger) *AssetStoreSQLite {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetStoreSQLite{log: log}
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
func (s *Service) Get(ctx context.Context, id string) (*Details, error) {
	return s.store.Get(ctx, id)
}

// List delegates to the wrapped Store.
func (s *Service) List(ctx context.Context, filter Filter) ([]*Summary, error) {
	return s.store.List(ctx, filter)
}

// Save delegates to the wrapped Store.
func (s *Service) Save(ctx context.Context, details *Details) error {
	return s.store.Save(ctx, details)
}

// Delete delegates to the wrapped Store.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// ── nil-returning factory stubs (HYBRID embed promotion) ────────────
//
// The four factory methods stay on the legacy struct so it satisfies
// the `assetStoreAdapter` interface (store_core.go) — they return nil
// in legacy execution paths. Local infra's
// `*sqassets.AssetStoreSQLite` declares its own concrete versions of
// these four methods, which shadow the legacy stubs and produce the
// real adapters.
//
// Why not delete these stubs: deleting them would break the
// `assetStoreAdapter` interface type-switch from going through struct
// embedding cleanly, plus regression tests in other packages may
// still construct legacy `*asset.AssetStoreSQLite` directly (and the
// legacy type would then lack the constructor-dispatch methods even
// after the LOCAL embed removed it).
func (s *AssetStoreSQLite) AssetRepository() Repository {
	return nil
}

func (s *AssetStoreSQLite) LocationRepository() LocationRepository {
	return nil
}

func (s *AssetStoreSQLite) ProcessingRepository() ProcessingRepository {
	return nil
}

func (s *AssetStoreSQLite) VersionRepository() VersionRepository {
	return nil
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
// `internal/infrastructure/database/sqlite/assets/clips_repository.go`
// (`MediaAssetColumns`, `ClipFolderColumns` — now exported) and
// `internal/infrastructure/database/sqlite/assets/store_helpers.go`
// (the production query builders). Domain retains the soft-delete
// SSOT only — no SQL primitives, no `database/sql` import.
func SoftDeleteFilter() string {
	return "lifecycle_state != 'DELETED'"
}

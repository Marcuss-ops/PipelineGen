// Package mediarepository exposes a database-agnostic contract for media asset
// persistence and ships a SQL-backed adapter over the existing SQLite
// implementation.
//
// Why this exists (Fase 0.5 of the migration roadmap):
//   - PipelineGen currently binds every service to *clips.Repository, a
//     concrete struct tightly coupled to SQLite via `database/sql` and
//     SQLite-specific JSON helpers (json_extract, json_set, ...).
//   - Migrating to PostgreSQL (Fase 2) without this seam would require
//     touching ~30 services. With it, only the SQLRepository adapter and
//     any future PostgresRepository need change.
//
// Implementations:
//   - SQLRepository (this package) — wraps *clips.Repository, no behavior
//     change; safe drop-in for back-compat with the SQLite-only setup today.
//   - PostgresRepository (planned, Fase 2) — backed by pgx or lib/pq with
//     Postgres-native SQL; implements the same MediaRepository interface.
//
// Method surface: chosen from grep evidence of `clips.Repository` usage in
// the three target services called out by the migration roadmap:
//
//	mediacurator  (internal/service/mediacurator/**)
//	scriptcore    (internal/service/scriptcore/clip_source.go)
//	clipresolver  (internal/media/clipcatalog/**) — uses its own
//	              clipcatalog.Repository; this MediaRepository is for the
//	              broader clips.* surface these services depend on.
//
// Methods are intentionally the *common* CRUD surface. Services that need
// niche expose (dedup, segment embeddings, scoring) can keep using the
// concrete *clips.Repository until a later phase extends the interface.
package mediarepository

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
)

// MediaRepository is the canonical, database-agnostic contract for media
// asset persistence. Every PipelineGen service that reads or writes clips
// should depend on this interface, not on a concrete *clips.Repository.
//
// Implementations:
//   - *SQLRepository       — SQLite (current)
//   - *PostgresRepository  — PostgreSQL (planned, Fase 2)
//
// NOTE: This interface deliberately omits DB-specific helpers
// (BeginTx, DB, Log) that leak SQL plumbing. Services that need a
// transaction can request it via a future WithTx method on the adapter,
// or fall back to the concrete type. Keeping the interface narrow makes
// a future Postgres port mechanical.
type MediaRepository interface {
	// ── CLIP CRUD ────────────────────────────────────────────────────────
	// Upsert inserts or updates a media asset in the `media_assets`
	// table. Existing rows are replaced via the repository's
	// conflict-resolution strategy (currently SQLite ON CONFLICT(id)).
	Upsert(ctx context.Context, clip *asset.MediaAsset) error

	// Get fetches a single media asset by its ID. Returns sql.ErrNoRows
	// (wrapped) if not found. Soft-deleted clips are excluded by default.
	Get(ctx context.Context, id string) (*asset.MediaAsset, error)

	// SoftDelete soft-deletes a clip (sets $.deleted_at in metadata_json).
	SoftDelete(ctx context.Context, id string) error

	// HardDelete permanently removes a clip row.
	HardDelete(ctx context.Context, id string) error

	// Restore undoes a soft-delete (json_remove $.deleted_at).
	Restore(ctx context.Context, id string) error

	// DeleteByDriveLink soft-deletes a clip by its Drive URL.
	// Used by Drive folder cleanup sweeps.
	DeleteByDriveLink(ctx context.Context, driveLink string) error

	// GetByDriveFileID fetches a clip by its Google Drive file ID.
	// Used by Drive ingest flows.
	GetByDriveFileID(ctx context.Context, driveFileID string) (*asset.MediaAsset, error)

	// FindByPHash returns the asset ID of a clip matching the perceptual-hash
	// fingerprint (or "" if no match). Used by deduplication sweeps.
	// Returns the ID (not the full asset) because phash lookups are hot-path
	// dedup probes — the caller usually has the full record locally and just
	// needs to confirm collision.
	FindByPHash(ctx context.Context, phash string) (string, error)

	// ── CLIP list operations ─────────────────────────────────────────────
	// List returns all clips, optionally filtered by source.
	// Used by association/providers.go to load the entire catalogue.
	List(ctx context.Context, source string) ([]*asset.MediaAsset, error)

	// ListPaged returns a page of clips with optional text query `q`.
	// Used by API handlers for clip listing endpoints.
	ListPaged(ctx context.Context, source string, limit, offset int, q string) ([]*asset.MediaAsset, error)

	// ListByFolderID returns all clips belonging to a logical folder.
	ListByFolderID(ctx context.Context, folderID string) ([]*asset.MediaAsset, error)

	// ListByFolderPath returns all clips under a Path-based folder
	// (used by source fallback logic).
	ListByFolderPath(ctx context.Context, folderPath string) ([]*asset.MediaAsset, error)

	// ── CLIP FOLDER CRUD ─────────────────────────────────────────────────
	// UpsertFolder creates or updates a folder row in `clip_folders`.
	UpsertFolder(ctx context.Context, folder *models.ClipFolder) error

	// GetFolder fetches a folder by its primary ID.
	GetFolder(ctx context.Context, folderID string) (*models.ClipFolder, error)

	// DeleteFolder removes a folder from `clip_folders`.
	DeleteFolder(ctx context.Context, id string) error
}

// SQLRepository is the SQLite-backed MediaRepository. It composes a
// *clips.Repository (which carries the actual SQL) and exposes every
// MediaRepository method through it.
//
// No behavior change vs. using *clips.Repository directly — this is a
// pure type-system seam to unblock the future Postgres migration.
type SQLRepository struct {
	inner *clips.Repository
	log   *zap.Logger
}

// NewSQLRepository wraps an existing *clips.Repository in a SQLRepository
// adapter that satisfies mediarepository.MediaRepository.
//
// Returns nil if inner is nil — services can short-circuit on a nil repo.
func NewSQLRepository(inner *clips.Repository, log *zap.Logger) *SQLRepository {
	if inner == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLRepository{inner: inner, log: log}
}

// Inner returns the underlying concrete *clips.Repository.
//
// This is intentionally NOT part of the MediaRepository interface — it is
// an escape hatch for niche operations (dedup, segment embeddings, scoring,
// transactional control via BeginTx) that the interface surface does not
// (yet) cover. Production services should prefer the interface methods.
func (r *SQLRepository) Inner() *clips.Repository {
	return r.inner
}

// Log returns the adapter's logger.
func (r *SQLRepository) Log() *zap.Logger {
	return r.log
}

// ── CLIP CRUD (delegate) ───────────────────────────────────────────────

func (r *SQLRepository) Upsert(ctx context.Context, clip *asset.MediaAsset) error {
	return r.inner.Upsert(ctx, clip)
}

func (r *SQLRepository) Get(ctx context.Context, id string) (*asset.MediaAsset, error) {
	return r.inner.Get(ctx, id)
}

func (r *SQLRepository) SoftDelete(ctx context.Context, id string) error {
	return r.inner.SoftDelete(ctx, id)
}

func (r *SQLRepository) HardDelete(ctx context.Context, id string) error {
	return r.inner.HardDelete(ctx, id)
}

func (r *SQLRepository) Restore(ctx context.Context, id string) error {
	return r.inner.Restore(ctx, id)
}

func (r *SQLRepository) DeleteByDriveLink(ctx context.Context, driveLink string) error {
	return r.inner.DeleteByDriveLink(ctx, driveLink)
}

func (r *SQLRepository) GetByDriveFileID(ctx context.Context, driveFileID string) (*asset.MediaAsset, error) {
	return r.inner.GetByDriveFileID(ctx, driveFileID)
}

func (r *SQLRepository) FindByPHash(ctx context.Context, phash string) (string, error) {
	return r.inner.FindByPHash(ctx, phash)
}

// ── CLIP list operations (delegate) ────────────────────────────────────

func (r *SQLRepository) List(ctx context.Context, source string) ([]*asset.MediaAsset, error) {
	return r.inner.ListClips(ctx, source)
}

func (r *SQLRepository) ListPaged(ctx context.Context, source string, limit, offset int, q string) ([]*asset.MediaAsset, error) {
	return r.inner.ListClipsPaged(ctx, source, limit, offset, q)
}

func (r *SQLRepository) ListByFolderID(ctx context.Context, folderID string) ([]*asset.MediaAsset, error) {
	return r.inner.ListByFolderID(ctx, folderID)
}

func (r *SQLRepository) ListByFolderPath(ctx context.Context, folderPath string) ([]*asset.MediaAsset, error) {
	return r.inner.ListByFolderPath(ctx, folderPath)
}

// ── CLIP FOLDER CRUD (delegate) ────────────────────────────────────────

func (r *SQLRepository) UpsertFolder(ctx context.Context, folder *models.ClipFolder) error {
	return r.inner.UpsertFolder(ctx, folder)
}

func (r *SQLRepository) GetFolder(ctx context.Context, folderID string) (*models.ClipFolder, error) {
	return r.inner.GetFolder(ctx, folderID)
}

func (r *SQLRepository) DeleteFolder(ctx context.Context, id string) error {
	return r.inner.DeleteFolder(ctx, id)
}

// ── Compile-time interface satisfaction check ──────────────────────────
//
// If SQLRepository stops implementing MediaRepository (e.g. a method is
// renamed or its signature drifts) this var fails to compile, immediately
// surfacing the contract break at the adapter boundary.
var _ MediaRepository = (*SQLRepository)(nil)

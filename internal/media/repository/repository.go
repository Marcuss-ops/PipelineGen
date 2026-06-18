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
	// UpsertClip inserts or updates a media asset in the `media_assets`
	// table. Existing rows are replaced via the repository's
	// conflict-resolution strategy (currently SQLite ON CONFLICT(id)).
	UpsertClip(ctx context.Context, clip *asset.MediaAsset) error

	// GetClip fetches a single media asset by its ID. Returns sql.ErrNoRows
	// (wrapped) if not found. Soft-deleted clips are excluded by default.
	GetClip(ctx context.Context, id string) (*asset.MediaAsset, error)

	// DeleteClip soft-deletes a clip (sets $.deleted_at in metadata_json).
	DeleteClip(ctx context.Context, id string) error

	// HardDeleteClip permanently removes a clip row.
	HardDeleteClip(ctx context.Context, id string) error

	// RestoreClip undoes a soft-delete (json_remove $.deleted_at).
	RestoreClip(ctx context.Context, id string) error

	// DeleteClipByDriveLink soft-deletes a clip by its Drive URL.
	// Used by Drive folder cleanup sweeps.
	DeleteClipByDriveLink(ctx context.Context, driveLink string) error

	// GetClipByDriveFileID fetches a clip by its Google Drive file ID.
	// Used by Drive ingest flows.
	GetClipByDriveFileID(ctx context.Context, driveFileID string) (*asset.MediaAsset, error)

	// FindByPHash returns the asset ID of a clip matching the perceptual-hash
	// fingerprint (or "" if no match). Used by deduplication sweeps.
	// Returns the ID (not the full asset) because phash lookups are hot-path
	// dedup probes — the caller usually has the full record locally and just
	// needs to confirm collision.
	FindByPHash(ctx context.Context, phash string) (string, error)

	// ── CLIP list operations ─────────────────────────────────────────────
	// ListClips returns all clips, optionally filtered by source.
	// Used by association/providers.go to load the entire catalogue.
	ListClips(ctx context.Context, source string) ([]*asset.MediaAsset, error)

	// ListClipsPaged returns a page of clips with optional text query `q`.
	// Used by API handlers for clip listing endpoints.
	ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*asset.MediaAsset, error)

	// ListClipsByFolderID returns all clips belonging to a logical folder.
	ListClipsByFolderID(ctx context.Context, folderID string) ([]*asset.MediaAsset, error)

	// ListClipsByFolderPath returns all clips under a Path-based folder
	// (used by source fallback logic).
	ListClipsByFolderPath(ctx context.Context, folderPath string) ([]*asset.MediaAsset, error)

	// ── CLIP FOLDER CRUD ─────────────────────────────────────────────────
	// UpsertClipFolder creates or updates a folder row in `clip_folders`.
	UpsertClipFolder(ctx context.Context, folder *models.ClipFolder) error

	// GetClipFolder fetches a folder by its primary ID.
	GetClipFolder(ctx context.Context, folderID string) (*models.ClipFolder, error)

	// DeleteClipFolder removes a folder from `clip_folders`.
	DeleteClipFolder(ctx context.Context, id string) error
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

func (r *SQLRepository) UpsertClip(ctx context.Context, clip *asset.MediaAsset) error {
	return r.inner.UpsertClip(ctx, clip)
}

func (r *SQLRepository) GetClip(ctx context.Context, id string) (*asset.MediaAsset, error) {
	return r.inner.GetClip(ctx, id)
}

func (r *SQLRepository) DeleteClip(ctx context.Context, id string) error {
	return r.inner.DeleteClip(ctx, id)
}

func (r *SQLRepository) HardDeleteClip(ctx context.Context, id string) error {
	return r.inner.HardDeleteClip(ctx, id)
}

func (r *SQLRepository) RestoreClip(ctx context.Context, id string) error {
	return r.inner.RestoreClip(ctx, id)
}

func (r *SQLRepository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	return r.inner.DeleteClipByDriveLink(ctx, driveLink)
}

func (r *SQLRepository) GetClipByDriveFileID(ctx context.Context, driveFileID string) (*asset.MediaAsset, error) {
	return r.inner.GetClipByDriveFileID(ctx, driveFileID)
}

func (r *SQLRepository) FindByPHash(ctx context.Context, phash string) (string, error) {
	return r.inner.FindByPHash(ctx, phash)
}

// ── CLIP list operations (delegate) ────────────────────────────────────

func (r *SQLRepository) ListClips(ctx context.Context, source string) ([]*asset.MediaAsset, error) {
	return r.inner.ListClips(ctx, source)
}

func (r *SQLRepository) ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*asset.MediaAsset, error) {
	return r.inner.ListClipsPaged(ctx, source, limit, offset, q)
}

func (r *SQLRepository) ListClipsByFolderID(ctx context.Context, folderID string) ([]*asset.MediaAsset, error) {
	return r.inner.ListClipsByFolderID(ctx, folderID)
}

func (r *SQLRepository) ListClipsByFolderPath(ctx context.Context, folderPath string) ([]*asset.MediaAsset, error) {
	return r.inner.ListClipsByFolderPath(ctx, folderPath)
}

// ── CLIP FOLDER CRUD (delegate) ────────────────────────────────────────

func (r *SQLRepository) UpsertClipFolder(ctx context.Context, folder *models.ClipFolder) error {
	return r.inner.UpsertClipFolder(ctx, folder)
}

func (r *SQLRepository) GetClipFolder(ctx context.Context, folderID string) (*models.ClipFolder, error) {
	return r.inner.GetClipFolder(ctx, folderID)
}

func (r *SQLRepository) DeleteClipFolder(ctx context.Context, id string) error {
	return r.inner.DeleteClipFolder(ctx, id)
}

// ── Compile-time interface satisfaction check ──────────────────────────
//
// If SQLRepository stops implementing MediaRepository (e.g. a method is
// renamed or its signature drifts) this var fails to compile, immediately
// surfacing the contract break at the adapter boundary.
var _ MediaRepository = (*SQLRepository)(nil)

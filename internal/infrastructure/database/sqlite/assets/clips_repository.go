package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// Compile-time assertion (Wave 16, June 2026; PR 11 followup
// June 2026): *ClipsRepository statically implements
// jobsoutbox.SourceVersionQuerier (pre-flight idempotency surface
// for the source_version supersede gate in IndexingHandler). Per
// AGENTS.md Pattern 0, the assertion lives at the adapter
// (infrastructure) home so port-drift bugs surface at compile
// time, not at the first index.requested replay.
//
// PR 11 follow-up: AssetSourceChecker interface (which exposed
// GetClip(ctx, id) (*asset.Asset, error)) was replaced with the
// narrower SourceVersionQuerier (SourceVersionFor(ctx, id)
// (string, error)). The replacement eliminates the producer-side
// ↔ consumer-side priority-chain drift that pre-PR-11-followup
// code carried (producer scanned via inline COALESCE; consumer
// walked the Asset struct — different chains, same name). Both
// sides now route through SourceVersionFor from
// source_version.go above, which is the single source of truth.
//
// Two-method-or-more port guidance (now vacated): the legacy note
// referenced the GetClip-only interface, but SourceVersionQuerier
// also has a single method. Adding a second method to the
// upstream port will trip this assertion and force the concrete
// to implement the new method — same compile-time behaviour under
// the new name. The same pattern as
// qdrant/search_adapter.go::var _ appsearch.VectorStorePort = ...
var _ jobsoutbox.SourceVersionQuerier = (*ClipsRepository)(nil)

const mediaAssetColumns = `
	id,
	COALESCE(source, '') AS source,
	COALESCE(name, '') AS name,
	COALESCE(tags, '[]') AS tags,
	COALESCE(tags_norm, '') AS tags_norm,
	COALESCE(embedding_json, '[]') AS embedding_json,
	COALESCE(duration_ms, 0) AS duration_ms,
	COALESCE(url, '') AS url,
	COALESCE(relative_path, '') AS relative_path,
	COALESCE(local_path, '') AS local_path,
	COALESCE(web_view_link, '') AS web_view_link,
	COALESCE(download_url, '') AS download_url,
	COALESCE(drive_file_id, '') AS drive_file_id,
	COALESCE(file_hash, '') AS file_hash,
	COALESCE(is_folder, 0) AS is_folder,
	COALESCE(depth, 0) AS depth,
	COALESCE(folder_id, '') AS folder_id,
	COALESCE(parent_folder_id, '') AS parent_folder_id,
	COALESCE(folder_path, '') AS folder_path,
	COALESCE(category, '') AS category,
	COALESCE(filename, '') AS filename,
	COALESCE(metadata_json, '{}') AS metadata_json,
	COALESCE(visual_embedding, '[]') AS visual_embedding,
	COALESCE(transcript_embedding, '[]') AS transcript_embedding,
	created_at,
	COALESCE(updated_at, '') AS updated_at,
	COALESCE(width, 0) AS width,
	COALESCE(height, 0) AS height,
	COALESCE(lifecycle_state, 'ACTIVE') AS lifecycle_state,
	COALESCE(deleted_at, '') AS deleted_at,
	COALESCE(error, '') AS error,
	COALESCE(thumb_url, '') AS thumb_url,
	COALESCE(phash, '') AS phash,
	COALESCE(search_text, '') AS search_text,
	COALESCE(scene_type, '') AS scene_type,
	COALESCE(quality_score, 0.0) AS quality_score,
	COALESCE(reuse_count, 0) AS reuse_count,
	COALESCE(last_used_at, '') AS last_used_at`

type ClipsRepository struct {
	*asset.AssetStoreSQLite
	db  *sql.DB
	log *zap.Logger
}

func NewClipsRepository(db *sql.DB, log *zap.Logger) *ClipsRepository {
	return &ClipsRepository{
		AssetStoreSQLite: asset.NewAssetStoreSQLite(db, log),
		db:               db,
		log:              log,
	}
}

func NewClipsRepositoryCanonical(db *sql.DB, log *zap.Logger, canonical any) *ClipsRepository {
	return NewClipsRepository(db, log)
}

// Upsert is the canonical low-level write path that ALL production
// callers eventually flow into (via AssetStoreSQLite.Save). It is
// public because the canonical asset.Repository wrapper calls it;
// the narrow API surface for callers is Upsert only.
//
// QDRANT-asset-mutation isolation (June 2026): Upsert itself
// bypasses the outbox. Production callers that need vector
// indexing MUST use outbox.Dispatcher.EnqueueAndIndex (which
// performs the UPSERT and outbox_events INSERT in a single
// atomic tx). Methods flagged with `//nolint:production` below
// are dispatcher-only entry points; the CI lint in
// scripts/ci-architectural-checks.sh bans them in
// internal/application + internal/api paths.
func (r *ClipsRepository) Upsert(ctx context.Context, m *asset.Asset) error {
	return r.AssetStoreSQLite.Save(ctx, &asset.Details{Asset: m})
}

func (r *ClipsRepository) Get(ctx context.Context, id string) (*asset.Asset, error) {
	details, err := r.AssetStoreSQLite.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if details == nil {
		return nil, nil
	}
	return details.Asset, nil
}

func (r *ClipsRepository) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return r.Get(ctx, id)
}

// SourceVersionFor is the PR 11 follow-up narrow port implementation
// consumed by the IndexingHandler source_version supersede gate.
// Delegates to the package-level helper (source_version.go) so the
// priority-chain semantics are owned by ONE function even though two
// upstream callers (this method + cmd/admin inline) flow through it.
//
// Returns sql.ErrNoRows unchanged so the upstream consumer can
// distinguish "row missing" from "row exists but empty fingerprint".
// Both paths fall through to "skip the gate, let IndexClip decide";
// the diagnostic value of distinguishing them lives in tests
// (TestSourceVersionFor_AssetNotFoundReturnsErrNoRows).
//
// Note: GetClip (above) remains because IndexDeleteHandler keeps it
// via the AssetDeleter interface — that's a separate concern
// (deletion rather than version lookup). Removing GetClip would
// trigger a separate refactor (AssetDeleter → AssetMutator) which
// is out of scope for the PR 11 followup.
func (r *ClipsRepository) SourceVersionFor(ctx context.Context, id string) (string, error) {
	return SourceVersionFor(ctx, r.db, id)
}

func (r *ClipsRepository) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	args := []any{}
	conds := []string{"1=1"}
	if filter.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.MediaType != "" {
		conds = append(conds, "media_type = ?")
		args = append(args, filter.MediaType)
	}
	if len(filter.States) > 0 {
		conds = append(conds, inClause(len(filter.States), "lifecycle_state"))
		for _, s := range filter.States {
			args = append(args, s)
		}
	}
	query := "SELECT COUNT(*) FROM media_assets WHERE " + strings.Join(conds, " AND ")
	var n int64
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (r *ClipsRepository) SoftDelete(ctx context.Context, id string) error {
	return r.AssetStoreSQLite.Delete(ctx, id)
}

// SetIndexState writes the canonical media_assets.index_state column
// (QDRANT-002 PR6 / migration 094). Called by IndexDeleteHandler for
// the DELETE_PENDING and DELETED transitions; the Delete path is the
// only consumer in production today, but the method is exposed as
// public because future worker bootstrap or operator tooling may
// need to flip state directly (QDRANT-005 alerting followup).
//
// No lifecycle_state filter — the caller is responsible for picking
// the right state at the right time. SoftDeleteFilter() is applied
// by callers that need to exclude tombstoned rows (e.g. live
// re-index tooling); IndexDeleteHandler does NOT need it because the
// pre-flight already short-circuits to success on lifecycle_state in
// {deleted, DELETED}.
//
// Idempotent: the column flip on an already-target-state row is a
// no-op write; the lease-fence on the outbox handler prevents the
// same worker from racing itself.
func (r *ClipsRepository) SetIndexState(ctx context.Context, id string, state asset.IndexState) error {
	if id == "" {
		return fmt.Errorf("clips.SetIndexState: id is required")
	}
	if state == "" {
		return fmt.Errorf("clips.SetIndexState: state is required (got empty string; use the canonical 7-state enum)")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET index_state = ?, index_state_updated_at = ? WHERE id = ?`,
		string(state), nowStr, id)
	if err != nil {
		return fmt.Errorf("clips.SetIndexState(%s, %s): %w", id, state, err)
	}
	return nil
}

// SetIndexStateTx is the tx-scoped mirror of SetIndexState added in
// QDRANT-002 PR7. Called by Dispatcher.EnqueueAndDelete to stamp
// index_state=DELETE_PENDING atomically inside the same tx as the
// outbox_events INSERT. The tx parameter MUST be non-nil — callers
// passing nil get an explicit error rather than a silent fall-back
// so a misuse shows up immediately, not in a downstream idempotency
// short-circuit.
//
// Idempotency: same as SetIndexState (column flip on already-target
// state is a no-op write). Yet each retry increments the updated_at
// stamp — that's intentional so dashboards see the retry traffic on
// tail-end log analysis without requiring a separate retry metric.
//
// Caller responsibilities (NOT enforced here because the tx is in
// flight — caller has the context too):
//  1. Validate state against the 7-state alphabet via state.Valid()
//     before invoking. SetIndexStateTx returns an error on empty +
//     any non-Valid() state for caller convenience; if PR7 callers
//     skip the check, this method's error is the last line of
//     defense.
//  2. Do NOT also call SetIndexState (non-tx) on the same id inside
//     this same logical operation. The two writes race on the tx
//     boundary — a non-tx write before commit is invisible to
//     readers after the tx rolls back, while a non-tx write after
//     commit clobbers the new state silently.
//
// SoftDeleteFilter is NOT applied here — the producer's stamp
// observes the actual id even if the row was previously handled,
// so a re-emitted delete event re-stamps DELETE_PENDING on a
// tombstoned row (the worker's pre-flight still catches the
// already-DELETED case and short-circuits).
func (r *ClipsRepository) SetIndexStateTx(ctx context.Context, tx *sql.Tx, id string, state asset.IndexState) error {
	if tx == nil {
		return fmt.Errorf("clips.SetIndexStateTx: tx is required (callers in production MUST supply the Dispatcher's tx; tests may build a tx via db.BeginTx)")
	}
	if id == "" {
		return fmt.Errorf("clips.SetIndexStateTx: id is required")
	}
	if state == "" {
		return fmt.Errorf("clips.SetIndexStateTx: state is required (got empty string; use the canonical 7-state enum)")
	}
	if !state.Valid() {
		return fmt.Errorf("clips.SetIndexStateTx: state %q is not a canonical IndexState — call sites in production must validate", state)
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET index_state = ?, index_state_updated_at = ? WHERE id = ?`,
		string(state), nowStr, id)
	if err != nil {
		return fmt.Errorf("clips.SetIndexStateTx(%s, %s): %w", id, state, err)
	}
	return nil
}

// ── Dangerous-mutation removal — Wave 22 task 5 / PR-CLIP-RAW-MUTATIONS ───
//
// Restore, HardDelete, and HardDeleteClip are REMOVED from this concrete
// repository as of PR-CLIP-RAW-MUTATIONS (June 2026). Their replacements live
// in the restricted tx-scoped package
// `internal/infrastructure/database/sqlite/assets/txmutation/`:
//
//   - txmutation.RestoreTx(ctx, tx, id)   — flips lifecycle_state back to
//                                          'ready' inside a caller-owned tx.
//   - txmutation.HardDeleteTx(ctx, tx, id) — physically removes the row
//                                          + dependent rows inside a
//                                          caller-owned tx.
//
// Rationale:
//   1. Production-reachability ban. The presence of public methods on
//      *assets.ClipsRepository made it trivial for non-canonical callers
//      to bypass the outbox + the InternalAdminPurge safety gate. Removing
//      them from this receiver breaks all direct-producer paths. The CI
//      lint (`scripts/ci-architectural-checks.sh::Check 5`) bans any
//      direct caller regardless.
//   2. Tx-scoped only. The replacements REQUIRE the caller to hold an
//      open *sql.Tx. The caller — `admin.PurgeService` in
//      `internal/infrastructure/database/sqlite/admin/purge.go::HardDeleteClip`
//      and `RestoreClip` — opens the tx, calls the tx-scoped primitive,
//      and commits. A future caller that skips the tx boundary won't
//      compile because *sql.Tx is a non-optional parameter.
//
// The internal/application/assets/mutations/AssetMutationPrimitives
// interface ALSO drops Restore/HardDelete (UpsertClip stays — fixtures
// rely on it). The outbox dispatcher, which already implements
// AssetMutationDispatcher (3-method, not Primitives), is unaffected.
//
// Reference: architecture/deprecations.yaml → PR-CLIP-RAW-MUTATIONS.

func (r *ClipsRepository) Canonical() *ClipsRepository {
	return r
}

func (r *ClipsRepository) SoftDeleteFilter() string {
	// PR 1 (June 2026, Lifecycle state SSOT): historical rows are
	// rewritten to canonical UPPERCASE by migration 101; writers no
	// longer emit lowercase 'deleted'. SoftDeleteFilter reduces to a
	// single equality check so future writers that re-introduce a
	// legacy stray casing surface immediately as a migration 101
	// failure rather than as a silent filter bypass.
	return "lifecycle_state != 'DELETED'"
}

func (r *ClipsRepository) Log() *zap.Logger { return r.log }
func (r *ClipsRepository) DB() *sql.DB      { return r.db }

func (r *ClipsRepository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
}

// UpsertClip upserts a clip through the low-level Save() path.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. Callers that need vector
// indexing MUST use outbox.Dispatcher.EnqueueAndIndex instead, which
// performs the UPSERT and outbox_events INSERT in a single atomic tx.
//
// QDRANT-asset-mutation isolation (June 2026): //nolint:production.
// Production callers (internal/application/**, internal/api/**)
// MUST NOT call this directly. The legitimate callers are:
//  1. The dispatcher itself, which wraps this call inside an
//     outbox transaction via UpsertClipTx + emits an outbox event
//     in the same tx (the canonical QDRANT-002 path).
//  2. The admin tool's InternalAdminPurge adapter, when
//     back-filling a row in a scenario where the worker pool is
//     offline; the admin path uses `assets.ClipsRepository.Upsert`
//     rather than this method (which is dispatcher-only).
//  3. Tests via the dispatcher stub or a bare `&Service{}` fixture
//     (test code paths are explicitly allowlisted by the CI lint).
//
// Removed from public API surfaces:
//   - artlist.AssetStore (search_core_test only)
//   - clips.ClipRepositoryPort (clip_ops.go only)
//   - sourcing.ClipStorePort (sourcing/service.go only)
//
// Per the user's verify-the-rg-test contract:
//
//	`rg 'UpsertClip\(' internal/application internal/api` returns
//	ZERO production hits (test hits allowed).
func (r *ClipsRepository) UpsertClip(ctx context.Context, clip *asset.Asset) error {
	return r.Upsert(ctx, clip)
}

func (r *ClipsRepository) GetByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	return r.GetClipByDriveFileID(ctx, fileID)
}

func (r *ClipsRepository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*asset.ClipFolder, error) {
	return r.GetFolderByVideoID(ctx, videoID)
}

// UpsertClipTx is the tx-scoped UPSERT used by outbox.Dispatcher.
// This method IS the outbox-compliant path — it executes inside the
// dispatcher's tx alongside the outbox_events INSERT. Callers outside
// the dispatcher MUST supply their own outbox event in the same tx.
func (r *ClipsRepository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	tagsJSON, _ := json.Marshal(clip.Tags)
	searchTermsJSON, _ := json.Marshal(clip.SearchTerms)
	metadataJSON, _ := json.Marshal(clip.Metadata)
	deletedAtStr := ""
	if clip.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*clip.DeletedAt)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, duration_ms, tags, search_terms,
			search_text, lifecycle_state, deleted_at, metadata_json,
			created_at, updated_at, folder_id, parent_folder_id, folder_path,
			scene_type, phash, last_used_at, quality_score, reuse_count,
			embedding_json, visual_embedding, transcript_embedding,
			drive_link, download_link, local_path, drive_file_id, file_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			name = excluded.name,
			filename = excluded.filename,
			media_type = excluded.media_type,
			category = excluded.category,
			group_name = excluded.group_name,
			url = excluded.url,
			clip_page_url = excluded.clip_page_url,
			thumbnail_url = excluded.thumbnail_url,
			duration_ms = excluded.duration_ms,
			tags = excluded.tags,
			search_terms = excluded.search_terms,
			search_text = excluded.search_text,
			lifecycle_state = excluded.lifecycle_state,
			deleted_at = excluded.deleted_at,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at,
			folder_id = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id,
			folder_path = excluded.folder_path,
			scene_type = excluded.scene_type,
			phash = excluded.phash,
			last_used_at = excluded.last_used_at,
			quality_score = excluded.quality_score,
			reuse_count = excluded.reuse_count,
			embedding_json = excluded.embedding_json,
			visual_embedding = excluded.visual_embedding,
			transcript_embedding = excluded.transcript_embedding,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			local_path = excluded.local_path,
			drive_file_id = excluded.drive_file_id,
			file_hash = excluded.file_hash
	`,
		clip.ID, string(clip.Source), clip.Name, clip.Filename, string(clip.MediaType), clip.Category, clip.Group,
		clip.SourceURL, clip.ClipPageURL, clip.ThumbnailURL, clip.Duration.Milliseconds(), string(tagsJSON), string(searchTermsJSON),
		clip.SearchText, string(clip.LifecycleState), deletedAtStr, string(metadataJSON),
		timeutil.FormatRFC3339(clip.CreatedAt), nowStr, clip.FolderID(), clip.ParentFolderID(), clip.FolderPath(),
		clip.SceneType(), clip.PHash(), clip.LastUsedAt(), clip.QualityScore(), clip.ReuseCount(),
		clip.EmbeddingJSON(), clip.VisualEmbedding(), clip.TranscriptEmbedding(),
		clip.DriveLink(), clip.DownloadLink(), clip.LocalPath(), clip.DriveFileID(), clip.FileHash(),
	)
	return err
}

func (r *ClipsRepository) DeleteClip(ctx context.Context, id string) error {
	return r.SoftDelete(ctx, id)
}

// Wave 22 (June 2026) PR-CLIP-RAW-MUTATIONS: HardDeleteClip REMOVED.
// The thin wrapper that delegated to *assets.ClipsRepository.HardDelete
// is gone. The InternalAdminPurge adapter now opens its own tx and
// calls txmutation.HardDeleteTx directly. See architecture/deprecations.yaml.

// DeleteClipByDriveLink soft-deletes by drive/download link.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. It flips lifecycle_state
// to 'deleted' without emitting an asset.index.delete_requested event,
// which means the Qdrant point is never cleaned up.
//
// Callers should use deletion.DeletionService.DeleteClip (which routes
// through outbox.Dispatcher.EnqueueAndDelete) or call the dispatcher
// directly.
func (r *ClipsRepository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'DELETED', deleted_at = ? WHERE drive_link = ? OR download_link = ?`,
		now, driveLink, driveLink)
	return err
}

// Wave 15 (June 2026) split-file ownership reminder:
//   - count helpers (CountAll, CountIndexed, CountIndexable,
//     CountPendingOutbox, CountDeadLetter, ListIndexedIDs) and
//     List live in clips_repository_queries.go
//   - folder helpers (UpsertFolder, GetFolder, GetFolderByVideoID,
//     GetFolderByPath, ListFolders, DriveFolderAttrs,
//     LookupDriveFolderIDBySourcePath, UpsertDriveFolder) live in
//     clips_repository_folders.go
//   - low-level helpers (StreamAssetIDs, inClause) and the
//     AdvancedSearch type aliases live in clips_repository_queries.go
//
// This file's QDRANT-001 contribution is documented in the comments
// above (DeleteClipByDriveLink, Restore, SetIndexState,
// SetIndexStateTx); it does NOT re-declare any methods moved by the
// Wave 15 split.

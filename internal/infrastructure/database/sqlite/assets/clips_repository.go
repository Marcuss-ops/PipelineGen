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

// Compile-time assertion (Wave 16, June 2026): *ClipsRepository
// statically implements jobsoutbox.AssetSourceChecker (pre-flight
// idempotency surface for the source_version supersede gate in
// IndexingHandler). Per AGENTS.md Pattern 0, the assertion lives
// at the adapter (infrastructure) home so port-drift bugs surface
// at compile time, not at the first index.requested replay.
//
// Two-method-or-more port: AssetSourceChecker currently exposes
// only GetClip; adding a second method to the upstream port will
// trip this assertion and force the concrete to implement the new
// method. The same pattern as qdrant/search_adapter.go::var _
// appsearch.VectorStorePort = ...
var _ jobsoutbox.AssetSourceChecker = (*ClipsRepository)(nil)

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
	COALESCE(lifecycle_state, 'ready') AS lifecycle_state,
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

// QDRANT-002 close-out (June 2026): the canonical write path is
// outbox.Dispatcher. The raw non-tx write methods on *ClipsRepository
// (Restore, HardDelete, UpsertClip, RestoreClip, HardDeleteClip,
// DeleteClipByDriveLink) are RESTRICTED — they remain as documented
// escape hatches ONLY for admin/operator tooling; production callers
// MUST route through Dispatcher.EnqueueAndIndex / EnqueueAndDelete /
// EnqueueAndRestore / EnqueueAndHardDelete.
//
// The CI gate scripts/ci-architectural-checks.sh::Check 2 catches any
// NEW production caller of these methods, so the surface cannot grow
// silently. Existing admin callers (cmd/admin/*) remain in the
// allowlist. See internal/application/assets/deletion/service.go and
// internal/application/assets/restore/service.go for the canonical
// application-level wrappers an admin handler should call.

// Restore flips lifecycle_state back to 'ready'.
func (r *ClipsRepository) Restore(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = NULL, updated_at = ? WHERE id = ?", nowStr, id)
	return err
}

// RestoreTx is the tx-scoped restore used by Dispatcher.EnqueueAndRestore
// (QDRANT-002 close-out, June 2026). It flips lifecycle_state back to
// 'ready' inside the dispatcher's tx, atomically with the outbox_events
// INSERT for reindex.
func (r *ClipsRepository) RestoreTx(ctx context.Context, tx *sql.Tx, id string) error {
	if tx == nil {
		return fmt.Errorf("clips.RestoreTx: tx is required")
	}
	if id == "" {
		return fmt.Errorf("clips.RestoreTx: id is required")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := tx.ExecContext(ctx, "UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = NULL, updated_at = ? WHERE id = ?", nowStr, id)
	return err
}

func (r *ClipsRepository) HardDelete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_locations WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_processing WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_versions WHERE asset_id = ?", id)
	_, err = tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// HardDeleteTx is the tx-scoped physical delete used by
// Dispatcher.EnqueueAndHardDelete (QDRANT-002 close-out, June 2026).
// It removes the media_assets row and related rows inside the dispatcher's
// tx, atomically with the outbox_events INSERT for Qdrant cleanup.
func (r *ClipsRepository) HardDeleteTx(ctx context.Context, tx *sql.Tx, id string) error {
	if tx == nil {
		return fmt.Errorf("clips.HardDeleteTx: tx is required")
	}
	if id == "" {
		return fmt.Errorf("clips.HardDeleteTx: id is required")
	}
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_locations WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_processing WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_versions WHERE asset_id = ?", id)
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id); err != nil {
		return err
	}
	return nil
}

func (r *ClipsRepository) Canonical() *ClipsRepository {
	return r
}

func (r *ClipsRepository) SoftDeleteFilter() string {
	return "lifecycle_state != 'deleted' AND lifecycle_state != 'DELETED'"
}

func (r *ClipsRepository) Log() *zap.Logger { return r.log }
func (r *ClipsRepository) DB() *sql.DB      { return r.db }

func (r *ClipsRepository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
}

// upsertClip is the private post-QDRANT-005 (TODO 6) escape hatch for
// callers that cannot route through outbox.Dispatcher — admin/backfill
// tooling in cmd/admin/**, and any operator-driven recovery scripts.
//
// QDRANT-005 close-out (TODO 6, June 2026): UpsertClip was a public
// production-write API surface that bypassed the outbox. Renamed to
// lowercase so production callers cannot use it accidentally. Production
// callers that need a targeted column update should use one of the
// narrow patch methods below (UpdateFileHash, UpdateDriveLocation,
// UpdateProcessingMetadata); production callers that need a full
// atomic ingest MUST route through outbox.Dispatcher.EnqueueAndIndex
// (which calls UpsertClipTx inside the dispatcher's tx).
//
// Acceptable callers (gate, posts aggregator):
//   - cmd/admin/qdrant_readiness.go — backfill ingestion tools.
//   - Future operator-driven recovery scripts (one-shot, audited).
//   - The ClipsRepository's own internal helpers that already ran.
//
// Production handler / use-case code MUST NOT call this method.
// CI gate scripts/ci-architectural-checks.sh::Check 2 is the canonical
// enforcement; any new caller outside the admin/* allowlist will fail CI.
func (r *ClipsRepository) upsertClip(ctx context.Context, clip *asset.Asset) error {
	return r.Upsert(ctx, clip)
}

// ── QDRANT-005 (TODO 6) narrow patch methods ──────────────────────────────────────────
//
// Each of these is a single-column (or two-column) UPDATE that DOES NOT
// touch lifecycle_state. They exist so production code can record a
// single piece of state on the asset without re-running the full
// UPSERT (which would silently overwrite any out-of-band state changes
// the dispatcher has committed since the last ingest).
//
// Non-tx (per spec): each UPDATE is a single statement with a
// well-defined boundary; the read-modify-write in
// UpdateProcessingMetadata uses an internal tx to make the JSON-merge
// atomic, but the API surface itself accepts only (ctx, ...) — no
// caller-supplied tx. Tx-scoped variants (UpdateFileHashTx,
// UpdateDriveLocationTx, UpdateProcessingMetadataTx) are deferred to
// TODO 7 for use inside batched outbox flows.
//
// RowsAffected() is checked on every UPDATE: 0 rows = asset not found,
// returned as a typed error so callers can distinguish a failed
// pre-condition from a successful no-op write on an unchanged row.
//
// updatePattern (atomicity): each method wraps its work in at most
// one tx (either explicit or implicit via BatchExec). The non-tx
// variants are safe because the SQL is a single statement;
// read-modify-write goes through internal tx for the metadata merge.

// UpdateFileHash patches media_assets.file_hash for an existing asset
// without touching lifecycle_state. QDRANT-005 (TODO 6): canonical
// narrow patch for the Drive-hash-recovery flow (verifyClip and
// similar) where the asset is already known + indexed and only the
// file_hash column needs to be backfilled.
func (r *ClipsRepository) UpdateFileHash(ctx context.Context, assetID, fileHash string) error {
	if assetID == "" {
		return fmt.Errorf("clips.UpdateFileHash: assetID is required")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET file_hash = ?, updated_at = ? WHERE id = ?`,
		fileHash, nowStr, assetID,
	)
	if err != nil {
		return fmt.Errorf("clips.UpdateFileHash(%s): %w", assetID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("clips.UpdateFileHash(%s) rows-affected: %w", assetID, err)
	}
	if n == 0 {
		return fmt.Errorf("clips.UpdateFileHash(%s): asset not found", assetID)
	}
	return nil
}

// UpdateDriveLocation patches the Drive destination columns for an
// existing asset (drive_file_id, drive_link) without touching
// lifecycle_state. QDRANT-005 (TODO 6): canonical narrow patch for
// the post-Drive-upload stamp flow where the file has just been
// uploaded via the Dispatcher and only the destination metadata
// needs to be recorded.
//
// Idempotent on same id: two calls with the same arguments write the
// same values (no diff signal in updated_at because both calls run
// within the same second). Callers that want a "first-time stamped"
// signal should use a transaction-spanning flow + lifecycle_state
// transition (out of scope for this method).
func (r *ClipsRepository) UpdateDriveLocation(ctx context.Context, assetID, driveID, driveURL string) error {
	if assetID == "" {
		return fmt.Errorf("clips.UpdateDriveLocation: assetID is required")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET drive_file_id = ?, drive_link = ?, updated_at = ? WHERE id = ?`,
		driveID, driveURL, nowStr, assetID,
	)
	if err != nil {
		return fmt.Errorf("clips.UpdateDriveLocation(%s): %w", assetID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("clips.UpdateDriveLocation(%s) rows-affected: %w", assetID, err)
	}
	if n == 0 {
		return fmt.Errorf("clips.UpdateDriveLocation(%s): asset not found", assetID)
	}
	return nil
}

// UpdateProcessingMetadata merges a single key into the
// media_assets.metadata_json JSON blob without touching lifecycle_state,
// file_hash, or any other column. QDRANT-005 (TODO 6): canonical narrow
// patch for post-processing flows that record a single piece of state
// without re-running the full UPSERT.
//
// Semantics:
//   - value == nil         → delete the key from the metadata map
//                            (consistent with map literal semantics).
//   - value != nil         → set the key (add or overwrite); existing
//                            other keys are preserved.
//   - metadata_json empty  → treated as {} so first-write works
//                            without a precondition INSERT.
//
// Read-modify-write cost: 1 SELECT + 1 UPDATE per call inside an
// internal tx for atomicity. Acceptable because this is invoked from
// tail-end processing flows (post-indexing, post-render), not hot-path
// ingestion. Tx-scoped variant (UpdateProcessingMetadataTx) is
// deferred to TODO 7.
func (r *ClipsRepository) UpdateProcessingMetadata(ctx context.Context, assetID, key string, value any) error {
	if assetID == "" {
		return fmt.Errorf("clips.UpdateProcessingMetadata: assetID is required")
	}
	if key == "" {
		return fmt.Errorf("clips.UpdateProcessingMetadata: key is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clips.UpdateProcessingMetadata(%s) begin tx: %w", assetID, err)
	}
	defer tx.Rollback()

	// Read current metadata_json (if any) so we can merge the new
	// key without blowing away existing keys. NULL / empty / "{}"
	// all map to "start from empty map".
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT metadata_json FROM media_assets WHERE id = ?`, assetID,
	).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("clips.UpdateProcessingMetadata(%s): asset not found", assetID)
		}
		return fmt.Errorf("clips.UpdateProcessingMetadata(%s) select: %w", assetID, err)
	}

	merged := map[string]any{}
	if raw.Valid && strings.TrimSpace(raw.String) != "" && raw.String != "{}" {
		if err := json.Unmarshal([]byte(raw.String), &merged); err != nil {
			return fmt.Errorf("clips.UpdateProcessingMetadata(%s) unmarshal existing metadata: %w", assetID, err)
		}
	}

	if value == nil {
		delete(merged, key)
	} else {
		merged[key] = value
	}

	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("clips.UpdateProcessingMetadata(%s) marshal merged: %w", assetID, err)
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET metadata_json = ?, updated_at = ? WHERE id = ?`,
		string(mergedJSON), nowStr, assetID,
	); err != nil {
		return fmt.Errorf("clips.UpdateProcessingMetadata(%s) update: %w", assetID, err)
	}
	return tx.Commit()
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

// RestoreClip is the legacy alias for Restore. See Restore's QDRANT-002 doc.
func (r *ClipsRepository) RestoreClip(ctx context.Context, id string) error {
	return r.Restore(ctx, id)
}

func (r *ClipsRepository) HardDeleteClip(ctx context.Context, id string) error {
	return r.HardDelete(ctx, id)
}

// DeleteClipByDriveLink soft-deletes by drive/download link via the
// raw media_assets UPDATE path.
//
// QDRANT-002 close-out: this method is RESTRICTED to admin/operator
// tooling (cmd/admin/*). Production callers MUST use outbox.Dispatcher.
// EnqueueAndDelete which emits the canonical asset.index.delete_requested.v1
// event the IndexDeleteHandler relies on (Qdrant cleanup + SoftDelete in
// the handler, NOT here). The CI gate Check 2 catches any new caller
// outside admin allowlist.
func (r *ClipsRepository) DeleteClipByDriveLink(ctx context.Context, driveLink string) error {
	driveLink = strings.TrimSpace(driveLink)
	if driveLink == "" {
		return fmt.Errorf("drive link is required")
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'deleted', deleted_at = ? WHERE drive_link = ? OR download_link = ?`,
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

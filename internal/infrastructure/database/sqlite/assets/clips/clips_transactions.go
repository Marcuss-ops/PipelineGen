package clips

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_transactions.go holds the tx-scoped *ClipsRepository methods:
// BeginTx opens a *sql.Tx from the underlying *sql.DB, UpsertClipTx
// performs the dispatcher's multi-column UPSERT inside the supplied
// tx (alongside the outbox_events INSERT it belongs to), and
// SetIndexStateTx writes the canonical index_state column flip
// inside a caller-supplied tx. Non-tx state transitions live in
// clips_index_state.go.

// BeginTx opens a *sql.Tx using the underlying *sql.DB. Callers MUST
// hold a reference to the returned tx for the duration of their
// transaction-bound work (UpsertClipTx, SetIndexStateTx, etc.) and
// MUST commit OR rollback explicitly — no internal goroutine
// retains the tx; the caller owns the lifecycle.
func (r *ClipsRepository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
}

// UpsertClipTx is the tx-scoped UPSERT used by outbox.Dispatcher.
// This method IS the outbox-compliant path — it executes inside the
// dispatcher's tx alongside the outbox_events INSERT. Callers outside
// the dispatcher MUST supply their own outbox event in the same tx.
func (r *ClipsRepository) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	// source_url convergence (godlike/06): the typed SourceURL field is the
	// canonical owner of the source URL; the legacy metadata key is a
	// provenance mirror. Stamp the mirror at the persistence boundary so
	// every row's metadata_json carries source_url for legacy readers
	// (FindBySourceURL dedup, Qdrant search-text) regardless of which
	// producer wrote it. Additive: never overwrites an existing key.
	if clip.SourceURL != "" && clip.MetadataSourceURL() == "" {
		clip.SetMetadataSourceURL(clip.SourceURL)
	}
	tagsJSON, _ := json.Marshal(clip.Tags)
	searchTermsJSON, _ := json.Marshal(clip.SearchTerms)
	metadataJSON, _ := json.Marshal(clip.Metadata)
	// Column writes prefer the field so url and source_url columns never
	// diverge; the metadata key remains the fallback for legacy rows.
	sourceProvider := clip.MetadataSourceProvider()
	sourceVideoID := clip.MetadataSourceVideoID()
	sourceURL := clip.SourceURL
	if sourceURL == "" {
		sourceURL = clip.MetadataSourceURL()
	}
	startMS := int64(asset.MetadataFloat(clip.Metadata, "start_sec") * 1000)
	endMS := int64(asset.MetadataFloat(clip.Metadata, "end_sec") * 1000)
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
			drive_link, download_link, local_path, drive_file_id, file_hash,
			source_provider, source_video_id, source_url, start_ms, end_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			file_hash = excluded.file_hash,
			source_provider = excluded.source_provider,
			source_video_id = excluded.source_video_id,
			source_url = excluded.source_url,
			start_ms = excluded.start_ms,
			end_ms = excluded.end_ms
	`,
		clip.ID, string(clip.Source), clip.Name, clip.Filename, string(clip.MediaType), clip.Category, clip.Group,
		clip.SourceURL, clip.ClipPageURL, clip.ThumbnailURL, clip.Duration.Milliseconds(), string(tagsJSON), string(searchTermsJSON),
		clip.SearchText, string(clip.LifecycleState), deletedAtStr, string(metadataJSON),
		timeutil.FormatRFC3339(clip.CreatedAt), nowStr, clip.FolderID(), clip.ParentFolderID(), clip.FolderPath(),
		clip.SceneType(), clip.PHash(), clip.LastUsedAt(), clip.QualityScore(), clip.ReuseCount(),
		clip.EmbeddingJSON(), clip.VisualEmbedding(), clip.TranscriptEmbedding(),
		clip.DriveLink(), clip.DownloadLink(), clip.LocalPath(), clip.DriveFileID(), clip.FileHash(),
		sourceProvider, sourceVideoID, sourceURL, startMS, endMS,
	)
	return err
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

// Package assetrepo is the canonical SQLite implementation of asset.Repository.
//
// It reads from media_assets directly — no metadata_json extract for
// canonical fields. The schema-level row reader lives in scanner.go
// (selectColumns + scanAsset). Provider-specific fields still live in
// metadata_json and are exposed via MediaAsset.Metadata.
//
// Location fields (local_path, drive_file_id, drive_link, download_link,
// file_hash) are written to the asset_locations satellite table — NOT
// into media_assets columns. This is the PR1 separation of concerns:
// media_assets = identity + metadata; asset_locations = physical files.
//
// Transactional outbox: every mutating method writes an outbox_events
// row in the SAME transaction as the data change. Consumers of the
// canonical pipeline must observe the outbox to react to asset changes
// (search index updates, downstream indexers, etc.).
//
// The implementation is goroutine-safe via standard database/sql
// connection pooling. Callers MUST NOT mutate the *sql.DB connection
// pool from outside.
package assetrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// Repository is the concrete SQLite implementation of asset.Repository.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// Compile-time interface check.
var _ assets.Repository = (*Repository)(nil)

// New returns a Repository backed by db.
func New(db *sql.DB, log *zap.Logger) *Repository {
	if log == nil {
		log = zap.NewNop()
	}
	return &Repository{db: db, log: log}
}

// ── CRUD ───────────────────────────────────────────────────────────────

// Upsert inserts or replaces a media_asset row and synchronises the
// canonical satellite tables (asset_locations). Writes "asset.upserted"
// outbox event in the same transaction.
//
// PR1: Legacy location columns are dual-written to media_assets for backward
// compatibility with callers that read drive_link, local_path, etc. from the
// main table. The canonical destination is asset_locations. Legacy columns
// will be dropped from media_assets in PR2.
//
// UpsertTx is the tx-aware variant — same logic without starting its own
// transaction or emitting outbox (caller controls both).
func (r *Repository) Upsert(ctx context.Context, m *assets.Asset) error {
	if m == nil {
		return assets.ErrInvalidID
	}
	if m.ID == "" {
		return assets.ErrInvalidID
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetrepo.Upsert begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now

	tagsJSON, _ := json.Marshal(m.Tags)
	searchTermsJSON, _ := json.Marshal(m.SearchTerms)

	// NOTE: ON CONFLICT(id) DO UPDATE does NOT include created_at in the
	// update clause → on re-upsert the row preserves its original
	// created_at even though we bind a fresh nowStr value.
	//
	// PR1: Legacy location columns (drive_file_id, drive_link, download_link,
	// local_path, relative_path, file_hash, embedding_json, visual_embedding,
	// transcript_embedding) are dual-written to media_assets for backward
	// compatibility. The canonical destination is asset_locations.
	// These columns will be dropped from media_assets in PR2.
	if err := upsertMediaAssetRow(ctx, tx, m, string(tagsJSON), string(searchTermsJSON), nowStr); err != nil {
		return fmt.Errorf("assetrepo.Upsert(%s): %w", m.ID, err)
	}

	// Synchronise asset_locations from the deprecated location fields.
	// These fields are still on assets.Asset for backward compat but
	// the canonical home is asset_locations.
	if err := upsertLocationRows(ctx, tx, m, nowStr); err != nil {
		return fmt.Errorf("assetrepo.Upsert(%s) locations: %w", m.ID, err)
	}

	if err := writeOutbox(ctx, tx, m.ID, "asset.upserted", m); err != nil {
		return fmt.Errorf("assetrepo.Upsert outbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("assetrepo.Upsert commit: %w", err)
	}
	return nil
}

// Get reads a single media_asset row by id. Returns (nil, nil) when missing.
// Soft-deleted rows return (nil, assets.ErrSoftDeleted) so callers can
// distinguish "not found" from "deleted".
func (r *Repository) Get(ctx context.Context, id string) (*assets.Asset, error) {
	if id == "" {
		return nil, assets.ErrInvalidID
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM media_assets WHERE id = ?`, id)
	m, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetrepo.Get(%s): %w", id, err)
	}
	if m.LifecycleState == assets.StateDeleted {
		return nil, assets.ErrSoftDeleted
	}
	return m, nil
}

// List returns assets matching filter. Uses real columns; no json_extract.
func (r *Repository) List(ctx context.Context, filter assets.Filter) ([]*assets.Asset, error) {
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
	if len(filter.IDs) > 0 {
		conds = append(conds, inClause(len(filter.IDs), "id"))
		for _, id := range filter.IDs {
			args = append(args, id)
		}
	}
	if len(filter.ExcludeIDs) > 0 {
		conds = append(conds, inClause(len(filter.ExcludeIDs), "id", "NOT"))
		for _, id := range filter.ExcludeIDs {
			args = append(args, id)
		}
	}
	if filter.IsFolder != nil {
		conds = append(conds, "is_folder = ?")
		args = append(args, boolToInt(*filter.IsFolder))
	}

	query := "SELECT " + selectColumns + " FROM media_assets WHERE " +
		joinAnd(conds) + " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assetrepo.List: %w", err)
	}
	defer rows.Close()

	var out []*assets.Asset
	for rows.Next() {
		m, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("assetrepo.List scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Count returns the number of rows matching filter (no pagination).
func (r *Repository) Count(ctx context.Context, filter assets.Filter) (int64, error) {
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
	query := "SELECT COUNT(*) FROM media_assets WHERE " + joinAnd(conds)
	var n int64
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("assetrepo.Count: %w", err)
	}
	return n, nil
}

// SoftDelete marks the asset as deleted (lifecycle_state='deleted', deleted_at=now)
// and writes "asset.deleted" outbox event in the same transaction.
func (r *Repository) SoftDelete(ctx context.Context, id string) error {
	if id == "" {
		return assets.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetrepo.SoftDelete begin: %w", err)
	}
	defer tx.Rollback()

	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET lifecycle_state = ?, deleted_at = ?, updated_at = ?
		WHERE id = ? AND lifecycle_state != ?
	`, assets.StateDeleted, nowStr, nowStr, id, assets.StateDeleted)
	if err != nil {
		return fmt.Errorf("assetrepo.SoftDelete(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return assets.ErrNotFound
	}
	if err := writeOutbox(ctx, tx, id, "asset.deleted", nil); err != nil {
		return fmt.Errorf("assetrepo.SoftDelete outbox: %w", err)
	}
	return tx.Commit()
}

// Restore reverses a soft-delete. Idempotent: a non-deleted asset is a no-op.
func (r *Repository) Restore(ctx context.Context, id string) error {
	if id == "" {
		return assets.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetrepo.Restore begin: %w", err)
	}
	defer tx.Rollback()

	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET lifecycle_state = ?, deleted_at = '', updated_at = ?
		WHERE id = ? AND lifecycle_state = ?
	`, assets.StateReady, nowStr, id, assets.StateDeleted)
	if err != nil {
		return fmt.Errorf("assetrepo.Restore(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return assets.ErrNotFound
	}
	if err := writeOutbox(ctx, tx, id, "asset.restored", nil); err != nil {
		return fmt.Errorf("assetrepo.Restore outbox: %w", err)
	}
	return tx.Commit()
}

// HardDelete removes the asset row permanently. Audit trail is gone.
// Writes outbox event "asset.hard_deleted" before deletion so downstream
// observers can clean up before the parent row vanishes.
func (r *Repository) HardDelete(ctx context.Context, id string) error {
	if id == "" {
		return assets.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetrepo.HardDelete begin: %w", err)
	}
	defer tx.Rollback()

	if err := writeOutbox(ctx, tx, id, "asset.hard_deleted", id); err != nil {
		return fmt.Errorf("assetrepo.HardDelete outbox: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_assets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("assetrepo.HardDelete(%s): %w", id, err)
	}
	return tx.Commit()
}

// ── Transactional API ────────────────────────────────────────────────

// Tx exposes the SQL transaction for compound operations (cross-table
// updates with a single outbox emit). Callers use Tx.OnCommit to schedule
// outbox writes that share the transaction.
type Tx struct {
	tx  *sql.Tx
	log *zap.Logger
}

// OnCommit schedules an outbox event to be written if the transaction
// commits successfully.
func (t *Tx) OnCommit(ctx context.Context, assetID, event string, payload any) error {
	return writeOutbox(ctx, t.tx, assetID, event, payload)
}

// Commit finalises the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback undoes the transaction.
func (t *Tx) Rollback() error { return t.tx.Rollback() }

// WithTx begins a transaction and runs fn with a Tx handle. If fn returns
// an error, the transaction is rolled back; otherwise it is committed.
// Outbox events scheduled via Tx.OnCommit share the transaction.
func (r *Repository) WithTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetrepo.WithTx begin: %w", err)
	}
	defer tx.Rollback()

	if err := fn(&Tx{tx: tx, log: r.log}); err != nil {
		return err
	}
	return tx.Commit()
}

// ── Asset locations sync (PR1: canonical separation) ─────────────────

// upsertMediaAssetRow is the core INSERT/UPDATE for the media_assets main row.
// It includes legacy location columns (drive_file_id, drive_link, download_link,
// local_path, relative_path, file_hash, embedding_json, visual_embedding,
// transcript_embedding) for backward compatibility. These columns will be
// dropped from media_assets in PR2.
//
// The caller controls the transaction and outbox emission.
func upsertMediaAssetRow(ctx context.Context, tx *sql.Tx, m *assets.Asset, tagsJSON, searchTermsJSON, nowStr string) error {
	lifecycle := string(m.LifecycleState)
	if lifecycle == "" {
		lifecycle = string(assets.StateReady)
	}
	deletedAtStr := ""
	if m.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*m.DeletedAt)
	}

	usableForJSON := mustJSONArray(m.UsableFor())
	avoidForJSON := mustJSONArray(m.AvoidFor())
	metadataJSON := m.MetadataJSON()

	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, external_url,
			duration_ms, tags, search_terms, search_text,
			lifecycle_state, deleted_at,
			quality_score, reuse_count, last_used_at,
			scene_type, metadata_json, is_folder, depth,
			folder_id, parent_folder_id, folder_path,
			usable_for, avoid_for, phash, child_count,
			drive_file_id, drive_link, download_link, local_path, relative_path, file_hash,
			embedding_json, visual_embedding, transcript_embedding, visual_embedding_json,
			tags_norm, drive_folder_id, thumb_url,
			created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?
		)
		ON CONFLICT(id) DO UPDATE SET
			source          = excluded.source,
			name            = excluded.name,
			filename        = excluded.filename,
			media_type      = excluded.media_type,
			category        = excluded.category,
			group_name      = excluded.group_name,
			url             = excluded.url,
			clip_page_url   = excluded.clip_page_url,
			thumbnail_url   = excluded.thumbnail_url,
			external_url    = excluded.external_url,
			duration_ms     = excluded.duration_ms,
			tags            = excluded.tags,
			search_terms    = excluded.search_terms,
			search_text     = excluded.search_text,
			lifecycle_state = excluded.lifecycle_state,
			deleted_at      = excluded.deleted_at,
			updated_at      = excluded.updated_at,
			quality_score   = excluded.quality_score,
			reuse_count     = excluded.reuse_count,
			last_used_at    = excluded.last_used_at,
			scene_type      = excluded.scene_type,
			metadata_json   = excluded.metadata_json,
			is_folder       = excluded.is_folder,
			depth           = excluded.depth,
			folder_id       = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id,
			folder_path     = excluded.folder_path,
			usable_for      = excluded.usable_for,
			avoid_for       = excluded.avoid_for,
			phash           = excluded.phash,
			child_count     = excluded.child_count,
			drive_file_id   = excluded.drive_file_id,
			drive_link      = excluded.drive_link,
			download_link   = excluded.download_link,
			local_path      = excluded.local_path,
			relative_path   = excluded.relative_path,
			file_hash       = excluded.file_hash,
			embedding_json  = excluded.embedding_json,
			visual_embedding = excluded.visual_embedding,
			transcript_embedding = excluded.transcript_embedding,
			visual_embedding_json = excluded.visual_embedding_json,
			tags_norm       = excluded.tags_norm,
			drive_folder_id = excluded.drive_folder_id,
			thumb_url       = excluded.thumb_url
	`,
		m.ID, string(m.Source), m.Name, m.Filename, string(m.MediaType), m.Category, m.Group,
		m.SourceURL, m.ClipPageURL, m.ThumbnailURL,		m.ExternalURL(),
		m.Duration.Milliseconds(), tagsJSON, searchTermsJSON, m.SearchText,
		lifecycle, deletedAtStr,
		m.QualityScore(), m.ReuseCount(), m.LastUsedAt(),
		m.SceneType(), metadataJSON, boolToInt(m.IsFolder()), m.Depth(),
		m.FolderID(), m.ParentFolderID(), m.FolderPath(),
		usableForJSON, avoidForJSON, m.PHash(), m.ChildCount(),
		m.DriveFileID(), m.DriveLink(), m.DownloadLink(), m.LocalPath(), m.LocalPath(), m.FileHash(),
		m.EmbeddingJSON(), m.VisualEmbedding(), m.TranscriptEmbedding(), m.VisualEmbeddingJSON(),
		tagsNorm(m.Tags), m.FolderID(), m.ThumbnailURL,
		nowStr, nowStr,
	)
	if err != nil {
		return fmt.Errorf("upsert media_assets row: %w", err)
	}
	return nil
}

// UpsertTx is the tx-aware variant of Upsert. It performs the same media_assets
// insert/update + asset_locations sync, but does NOT start its own transaction
// or emit outbox events. The caller owns the transaction lifecycle and outbox
// emission. Use this when composing multi-table writes atomically.
func (r *Repository) UpsertTx(ctx context.Context, tx *sql.Tx, m *assets.Asset) error {
	if m == nil {
		return assets.ErrInvalidID
	}
	if m.ID == "" {
		return assets.ErrInvalidID
	}

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now

	tagsJSON, _ := json.Marshal(m.Tags)
	searchTermsJSON, _ := json.Marshal(m.SearchTerms)

	if err := upsertMediaAssetRow(ctx, tx, m, string(tagsJSON), string(searchTermsJSON), nowStr); err != nil {
		return fmt.Errorf("assetrepo.UpsertTx(%s): %w", m.ID, err)
	}

	if err := upsertLocationRows(ctx, tx, m, nowStr); err != nil {
		return fmt.Errorf("assetrepo.UpsertTx(%s) locations: %w", m.ID, err)
	}

	return nil
}

// upsertLocationRows writes location data from the deprecated fields on
// assets.Asset into the asset_locations satellite table. It runs
// inside the caller's transaction.
//
// Mapping:
//
//	LocalPath        → location_kind='local',  uri=LocalPath
//	DriveFileID      → location_kind='drive',  external_id=DriveFileID
//	DriveLink        → location_kind='drive',  access_url=DriveLink
//	DownloadLink     → location_kind='drive',  download_url=DownloadLink
//	FileHash         → both kinds inherit FileHash when set
//
// is_primary: local wins when LocalPath is non-empty; otherwise drive wins.
// Before upserting, stale rows whose deprecated field is now empty are
// deleted, and all other rows for this asset have is_primary cleared so
// the INSERT's is_primary value is the sole authority.
func upsertLocationRows(ctx context.Context, tx *sql.Tx, m *assets.Asset, nowStr string) error {
	// ── Local location ──────────────────────────────────────────────
	if m.LocalPath() != "" {
		// Reset any previous primary before setting the new one.
		if _, err := tx.ExecContext(ctx,
			`UPDATE asset_locations SET is_primary = 0 WHERE asset_id = ?`,
			m.ID); err != nil {
			return fmt.Errorf("reset primary before local upsert: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO asset_locations (
				asset_id, location_kind, uri, file_hash, is_primary,
				created_at, updated_at
			) VALUES (?, 'local', ?, ?, 1, ?, ?)
			ON CONFLICT(asset_id, location_kind) DO UPDATE SET
				uri         = excluded.uri,
				file_hash   = excluded.file_hash,
				is_primary  = excluded.is_primary,
				updated_at  = excluded.updated_at
		`, m.ID, m.LocalPath(), m.FileHash(), nowStr, nowStr)
		if err != nil {
			return fmt.Errorf("upsert local location: %w", err)
		}
	} else {
		// LocalPath became empty — remove the stale row.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = 'local'`,
			m.ID); err != nil {
			return fmt.Errorf("delete stale local location: %w", err)
		}
	}

	// ── Drive location ─────────────────────────────────────────────
	hasDrive := m.DriveFileID() != "" || m.DriveLink() != ""
	if hasDrive {
		uri := ""
		if m.DriveFileID() != "" {
			uri = "drive://" + m.DriveFileID()
		} else {
			uri = m.DriveLink()
		}
		// Drive is primary only when there's no local path.
		isPrimary := 0
		if m.LocalPath() == "" {
			// Reset previous primary before setting the new one.
			if _, err := tx.ExecContext(ctx,
				`UPDATE asset_locations SET is_primary = 0 WHERE asset_id = ?`,
				m.ID); err != nil {
				return fmt.Errorf("reset primary before drive upsert: %w", err)
			}
			isPrimary = 1
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO asset_locations (
				asset_id, location_kind, uri, external_id, web_view_link,
				download_url, file_hash, is_primary, created_at, updated_at
			) VALUES (?, 'drive', ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_id, location_kind) DO UPDATE SET
				uri           = excluded.uri,
				external_id   = excluded.external_id,
				web_view_link = excluded.web_view_link,
				download_url  = excluded.download_url,
				file_hash     = excluded.file_hash,
				is_primary    = excluded.is_primary,
				updated_at    = excluded.updated_at
		`, m.ID, uri, m.DriveFileID(), m.DriveLink(), m.DownloadLink(),
			m.FileHash(), isPrimary, nowStr, nowStr)
		if err != nil {
			return fmt.Errorf("upsert drive location: %w", err)
		}
	} else {
		// Drive fields became empty — remove the stale row.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = 'drive'`,
			m.ID); err != nil {
			return fmt.Errorf("delete stale drive location: %w", err)
		}
	}

	return nil
}

// ── Internal helpers ──────────────────────────────────────────────────

// writeOutbox enters one row into outbox_events, transaction-shared with
// the caller's mutating SQL. Same signature as writeOutbox in jobs/events.go
// so callers can compose both pipelines.
func writeOutbox(ctx context.Context, tx *sql.Tx, aggregateID, event string, payload any) error {
	var payloadJSON []byte
	if payload == nil {
		payloadJSON = []byte("{}")
	} else {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal outbox payload: %w", err)
		}
		payloadJSON = b
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, outboxID(), aggregateID, event, string(payloadJSON),
		timeutil.FormatRFC3339(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("write outbox row: %w", err)
	}
	return nil
}

// outboxID matches the project convention used in jobs/events.go (UnixNano
// + RandomString suffix). Avoids collisions on retries within the same
// nanosecond.
func outboxID() string {
	return fmt.Sprintf("outbox_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func mustJSONArray(xs []string) string {
	if xs == nil {
		return "[]"
	}
	b, err := json.Marshal(xs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// inClause builds "col IN (?, ?, ?)" placeholders. When negate is true
// (e.g. NOT IN), the prefix "NOT " is omitted — caller concatenates it.
func inClause(n int, col string, negate ...string) string {
	prefix := ""
	if len(negate) > 0 {
		prefix = negate[0] + " "
	}
	placeholders := make([]string, n)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return prefix + col + " IN (" + strings.Join(placeholders, ",") + ")"
}

// tagsNorm converts a tag list to a lowercase, accent-stripped string used
// for the denormalized tags_norm column (legacy compat; PR2 will drop it).
func tagsNorm(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		low = strings.NewReplacer(
			"à", "a", "è", "e", "é", "e", "ì", "i", "ò", "o", "ù", "u",
		).Replace(low)
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(low)
	}
	return b.String()
}

// joinAnd joins non-empty SQL conditions with " AND ".
func joinAnd(conds []string) string {
	out := ""
	for i, c := range conds {
		if i == 0 {
			out = c
		} else {
			out += " AND " + c
		}
	}
	return out
}

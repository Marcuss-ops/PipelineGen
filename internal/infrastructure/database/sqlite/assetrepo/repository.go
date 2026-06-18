// Package assetrepo is the canonical SQLite implementation of asset.Repository.
//
// It reads from media_assets directly — no metadata_json extract for
// canonical fields. The schema-level row reader lives in scanner.go
// (selectColumns + scanAsset). Provider-specific fields still live in
// metadata_json and are exposed via MediaAsset.Metadata.
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

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
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
var _ asset.Repository = (*Repository)(nil)

// New returns a Repository backed by db.
func New(db *sql.DB, log *zap.Logger) *Repository {
	if log == nil {
		log = zap.NewNop()
	}
	return &Repository{db: db, log: log}
}

// ── CRUD ───────────────────────────────────────────────────────────────

// Upsert inserts or replaces a media_asset row. Writes "asset.upserted"
// outbox event in the same transaction. Reads/writes use real columns
// (no json_extract for canonical fields).
func (r *Repository) Upsert(ctx context.Context, m *asset.MediaAsset) error {
	if m == nil {
		return asset.ErrInvalidID
	}
	if m.ID == "" {
		return asset.ErrInvalidID
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
	_, err = tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, external_url,
			duration_ms, tags, search_terms, search_text,
			lifecycle_state, deleted_at,
			quality_score, reuse_count, last_used_at,
			scene_type, metadata_json, is_folder, depth,
			folder_id, parent_folder_id, folder_path,
			usable_for, avoid_for, phash, child_count,
			status, error,
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
			?, ?,
			?, ?
		)
		ON CONFLICT(id) DO UPDATE SET
			source         = excluded.source,
			name           = excluded.name,
			filename       = excluded.filename,
			media_type     = excluded.media_type,
			category       = excluded.category,
			group_name     = excluded.group_name,
			url            = excluded.url,
			clip_page_url  = excluded.clip_page_url,
			thumbnail_url  = excluded.thumbnail_url,
			external_url   = excluded.external_url,
			duration_ms    = excluded.duration_ms,
			tags           = excluded.tags,
			search_terms   = excluded.search_terms,
			search_text    = excluded.search_text,
			lifecycle_state= excluded.lifecycle_state,
			deleted_at     = excluded.deleted_at,
			updated_at     = excluded.updated_at,
			quality_score  = excluded.quality_score,
			reuse_count    = excluded.reuse_count,
			last_used_at   = excluded.last_used_at,
			scene_type     = excluded.scene_type,
			metadata_json  = excluded.metadata_json,
			is_folder      = excluded.is_folder,
			depth          = excluded.depth,
			folder_id      = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id,
			folder_path    = excluded.folder_path,
			usable_for     = excluded.usable_for,
			avoid_for      = excluded.avoid_for,
			phash          = excluded.phash,
			child_count    = excluded.child_count,
			status         = excluded.status,
			error          = excluded.error
	`,
		// Values (matches the 35 ? placeholders above in order)
		m.ID, m.Source, m.Name, m.Filename, m.MediaType, m.Category, m.Group,
		m.SourceURL, m.ClipPageURL, m.ThumbnailURL, m.ExternalURL,
		m.DurationMs, string(tagsJSON), string(searchTermsJSON), m.SearchText,
		string(m.LifecycleState), timeutil.FormatPtrRFC3339(m.DeletedAt),
		m.QualityScore, m.ReuseCount, m.LastUsedAt,
		m.SceneType, m.MetadataJSON(), boolToInt(m.IsFolder), m.Depth,
		m.FolderID, m.ParentFolderID, m.FolderPath,
		mustJSONArray(m.UsableFor), mustJSONArray(m.AvoidFor), m.PHash, m.ChildCount,
		m.Status, m.Error,
		nowStr, nowStr,
	)
	if err != nil {
		return fmt.Errorf("assetrepo.Upsert(%s): %w", m.ID, err)
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
// Soft-deleted rows return (nil, asset.ErrSoftDeleted) so callers can
// distinguish "not found" from "deleted".
func (r *Repository) Get(ctx context.Context, id string) (*asset.MediaAsset, error) {
	if id == "" {
		return nil, asset.ErrInvalidID
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM media_assets WHERE id = ?`, id)
	m, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetrepo.Get(%s): %w", id, err)
	}
	if m.LifecycleState == asset.StateDeleted {
		return nil, asset.ErrSoftDeleted
	}
	return m, nil
}

// List returns assets matching filter. Uses real columns; no json_extract.
func (r *Repository) List(ctx context.Context, filter asset.Filter) ([]*asset.MediaAsset, error) {
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

	var out []*asset.MediaAsset
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
func (r *Repository) Count(ctx context.Context, filter asset.Filter) (int64, error) {
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
		return asset.ErrInvalidID
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
	`, asset.StateDeleted, nowStr, nowStr, id, asset.StateDeleted)
	if err != nil {
		return fmt.Errorf("assetrepo.SoftDelete(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return asset.ErrNotFound
	}
	if err := writeOutbox(ctx, tx, id, "asset.deleted", nil); err != nil {
		return fmt.Errorf("assetrepo.SoftDelete outbox: %w", err)
	}
	return tx.Commit()
}

// Restore reverses a soft-delete. Idempotent: a non-deleted asset is a no-op.
func (r *Repository) Restore(ctx context.Context, id string) error {
	if id == "" {
		return asset.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetrepo.Restore begin: %w", err)
	}
	defer tx.Rollback()

	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET lifecycle_state = ?, deleted_at = NULL, updated_at = ?
		WHERE id = ? AND lifecycle_state = ?
	`, asset.StateReady, nowStr, id, asset.StateDeleted)
	if err != nil {
		return fmt.Errorf("assetrepo.Restore(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return asset.ErrNotFound
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
		return asset.ErrInvalidID
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



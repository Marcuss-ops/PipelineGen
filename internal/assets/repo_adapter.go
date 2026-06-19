package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/files"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

type repositoryAdapter struct {
	store *AssetStoreSQLite
}

// AssetRepository returns the Repository adapter for the store.
func (s *AssetStoreSQLite) AssetRepository() Repository {
	return &repositoryAdapter{store: s}
}

// Compile-time interface check.
var _ Repository = (*repositoryAdapter)(nil)

func (a *repositoryAdapter) Upsert(ctx context.Context, m *Asset) error {
	if m == nil {
		return ErrInvalidID
	}
	if m.ID == "" {
		return ErrInvalidID
	}

	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assets.Upsert begin: %w", err)
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

	if err := upsertMediaAssetRow(ctx, tx, m, string(tagsJSON), string(searchTermsJSON), nowStr); err != nil {
		return fmt.Errorf("assets.Upsert(%s): %w", m.ID, err)
	}

	if err := upsertLocationRows(ctx, tx, m, nowStr); err != nil {
		return fmt.Errorf("assets.Upsert(%s) locations: %w", m.ID, err)
	}

	if err := writeOutbox(ctx, tx, m.ID, "asset.upserted", m); err != nil {
		return fmt.Errorf("assets.Upsert outbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("assets.Upsert commit: %w", err)
	}
	return nil
}

func (a *repositoryAdapter) Get(ctx context.Context, id string) (*Asset, error) {
	if id == "" {
		return nil, ErrInvalidID
	}
	row := a.store.db.QueryRowContext(ctx, `SELECT `+mediaAssetColumns+` FROM media_assets WHERE id = ?`, id)
	m, err := scanMediaAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assets.Get(%s): %w", id, err)
	}
	if m.LifecycleState == StateDeleted {
		return nil, ErrSoftDeleted
	}
	return m, nil
}

func (a *repositoryAdapter) List(ctx context.Context, filter Filter) ([]*Asset, error) {
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
		isFolderInt := 0
		if *filter.IsFolder {
			isFolderInt = 1
		}
		args = append(args, isFolderInt)
	}

	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE " +
		strings.Join(conds, " AND ") + " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	}

	rows, err := a.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assets.List: %w", err)
	}
	defer rows.Close()

	var out []*Asset
	for rows.Next() {
		m, err := scanMediaAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("assets.List scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *repositoryAdapter) Count(ctx context.Context, filter Filter) (int64, error) {
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
	if err := a.store.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("assets.Count: %w", err)
	}
	return n, nil
}

func (a *repositoryAdapter) SoftDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidID
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assets.SoftDelete begin: %w", err)
	}
	defer tx.Rollback()

	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET lifecycle_state = ?, deleted_at = ?, updated_at = ?
		WHERE id = ? AND lifecycle_state != ?
	`, StateDeleted, nowStr, nowStr, id, StateDeleted)
	if err != nil {
		return fmt.Errorf("assets.SoftDelete(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	if err := writeOutbox(ctx, tx, id, "asset.deleted", nil); err != nil {
		return fmt.Errorf("assets.SoftDelete outbox: %w", err)
	}
	return tx.Commit()
}

func (a *repositoryAdapter) Restore(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidID
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assets.Restore begin: %w", err)
	}
	defer tx.Rollback()

	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET lifecycle_state = ?, deleted_at = '', updated_at = ?
		WHERE id = ? AND lifecycle_state = ?
	`, StateReady, nowStr, id, StateDeleted)
	if err != nil {
		return fmt.Errorf("assets.Restore(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	if err := writeOutbox(ctx, tx, id, "asset.restored", nil); err != nil {
		return fmt.Errorf("assets.Restore outbox: %w", err)
	}
	return tx.Commit()
}

func (a *repositoryAdapter) HardDelete(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidID
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assets.HardDelete begin: %w", err)
	}
	defer tx.Rollback()

	if err := writeOutbox(ctx, tx, id, "asset.hard_deleted", nil); err != nil {
		return fmt.Errorf("assets.HardDelete outbox: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM media_assets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("assets.HardDelete(%s): %w", id, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrNotFound
	}

	return tx.Commit()
}

// ── Helpers ─────────────────────────────────────────────────────────

func upsertMediaAssetRow(ctx context.Context, tx *sql.Tx, m *Asset, tagsJSON, searchTermsJSON, nowStr string) error {
	lifecycle := string(m.LifecycleState)
	if lifecycle == "" {
		lifecycle = string(StateReady)
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
		m.SourceURL, m.ClipPageURL, m.ThumbnailURL, m.ExternalURL(),
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

func upsertLocationRows(ctx context.Context, tx *sql.Tx, m *Asset, nowStr string) error {
	if m.LocalPath() != "" {
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
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = 'local'`,
			m.ID); err != nil {
			return fmt.Errorf("delete stale local location: %w", err)
		}
	}

	hasDrive := m.DriveFileID() != "" || m.DriveLink() != ""
	if hasDrive {
		uri := ""
		if m.DriveFileID() != "" {
			uri = "drive://" + m.DriveFileID()
		} else {
			uri = m.DriveLink()
		}
		isPrimary := 0
		if m.LocalPath() == "" {
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
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = 'drive'`,
			m.ID); err != nil {
			return fmt.Errorf("delete stale drive location: %w", err)
		}
	}

	return nil
}

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
	`, fmt.Sprintf("outbox_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6)), aggregateID, event, string(payloadJSON),
		timeutil.FormatRFC3339(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("write outbox row: %w", err)
	}
	return nil
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

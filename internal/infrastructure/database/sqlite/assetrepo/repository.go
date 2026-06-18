package assetrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Repository implements asset.Repository on top of SQLite.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// New creates a new SQLite-backed asset repository.
func New(db *sql.DB, log *zap.Logger) *Repository {
	return &Repository{db: db, log: log}
}

// Upsert inserts or updates an asset row.
func (r *Repository) Upsert(ctx context.Context, a *asset.MediaAsset) error {
	tagsJSON, err := json.Marshal(a.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	searchTermsJSON, err := json.Marshal(a.SearchTerms)
	if err != nil {
		return fmt.Errorf("marshal search_terms: %w", err)
	}
	usableForJSON, err := json.Marshal(a.UsableFor)
	if err != nil {
		return fmt.Errorf("marshal usable_for: %w", err)
	}
	avoidForJSON, err := json.Marshal(a.AvoidFor)
	if err != nil {
		return fmt.Errorf("marshal avoid_for: %w", err)
	}
	metadataJSON := a.MetadataJSON()
	tagsNorm := normalizeTags(a.Tags)

	deletedAtStr := ""
	if a.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*a.DeletedAt)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, external_url,
			duration_ms, tags, tags_norm, search_terms, search_text,
			lifecycle_state, deleted_at,
			metadata_json, embedding_json, visual_embedding, transcript_embedding,
			visual_embedding_json, folder_id, parent_folder_id, folder_path,
			depth, is_folder, scene_type, quality_score, reuse_count, last_used_at,
			usable_for, avoid_for, phash, child_count,
			status, error, drive_file_id, drive_link, download_link,
			local_path, file_hash,
			created_at, updated_at
		) VALUES (
			?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?
		)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source, name=excluded.name, filename=excluded.filename,
			media_type=excluded.media_type, category=excluded.category,
			group_name=excluded.group_name,
			url=excluded.url, clip_page_url=excluded.clip_page_url,
			thumbnail_url=excluded.thumbnail_url, external_url=excluded.external_url,
			duration_ms=excluded.duration_ms, tags=excluded.tags, tags_norm=excluded.tags_norm,
			search_terms=excluded.search_terms, search_text=excluded.search_text,
			lifecycle_state=excluded.lifecycle_state, deleted_at=excluded.deleted_at,
			metadata_json=excluded.metadata_json, embedding_json=excluded.embedding_json,
			visual_embedding=excluded.visual_embedding,
			transcript_embedding=excluded.transcript_embedding,
			visual_embedding_json=excluded.visual_embedding_json,
			folder_id=excluded.folder_id, parent_folder_id=excluded.parent_folder_id,
			folder_path=excluded.folder_path, depth=excluded.depth,
			is_folder=excluded.is_folder, scene_type=excluded.scene_type,
			quality_score=excluded.quality_score, reuse_count=excluded.reuse_count,
			last_used_at=excluded.last_used_at,
			usable_for=excluded.usable_for, avoid_for=excluded.avoid_for,
			phash=excluded.phash, child_count=excluded.child_count,
			status=excluded.status, error=excluded.error,
			drive_file_id=excluded.drive_file_id, drive_link=excluded.drive_link,
			download_link=excluded.download_link,
			local_path=excluded.local_path, file_hash=excluded.file_hash,
			updated_at=excluded.updated_at
	`, a.ID, a.Source, a.Name, a.Filename, a.MediaType, a.Category, a.Group,
		a.SourceURL, a.ClipPageURL, a.ThumbnailURL, a.ExternalURL,
		a.DurationMs, string(tagsJSON), tagsNorm, string(searchTermsJSON), a.SearchText,
		string(a.LifecycleState), deletedAtStr,
		metadataJSON, a.EmbeddingJSON, a.VisualEmbedding, a.TranscriptEmbedding,
		a.VisualEmbeddingJSON, a.FolderID, a.ParentFolderID, a.FolderPath,
		a.Depth, boolToInt(a.IsFolder), a.SceneType, a.QualityScore, a.ReuseCount, a.LastUsedAt,
		string(usableForJSON), string(avoidForJSON), a.PHash, a.ChildCount,
		a.Status, a.Error, a.DriveFileID, a.DriveLink, a.DownloadLink,
		a.LocalPath, a.FileHash,
		a.CreatedAt, a.UpdatedAt,
	)
	return err
}

// Get returns a single asset by ID, or asset.ErrNotFound if not found.
func (r *Repository) Get(ctx context.Context, id string) (*asset.MediaAsset, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+selectColumns+" FROM media_assets WHERE id = ?", id)
	a, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, asset.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// List returns assets matching the filter.
func (r *Repository) List(ctx context.Context, f asset.Filter) ([]*asset.MediaAsset, error) {
	query, args := r.buildListQuery(f)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*asset.MediaAsset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Count returns the number of assets matching the filter.
func (r *Repository) Count(ctx context.Context, f asset.Filter) (int64, error) {
	query, args := r.buildCountQuery(f)
	var n int64
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// SoftDelete sets lifecycle_state='deleted' and deleted_at.
func (r *Repository) SoftDelete(ctx context.Context, id string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'deleted', deleted_at = ?,
		     metadata_json = json_set(COALESCE(metadata_json,'{}'), '$.deleted_at', ?)
		 WHERE id = ?`, now, now, id)
	return err
}

// Restore sets lifecycle_state='ready' and clears deleted_at.
func (r *Repository) Restore(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets
		 SET lifecycle_state = 'ready', deleted_at = '',
		     metadata_json = json_remove(COALESCE(metadata_json,'{}'), '$.deleted_at')
		 WHERE id = ?`, id)
	return err
}

// HardDelete permanently removes a row.
func (r *Repository) HardDelete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id)
	return err
}

// ── query builders ──────────────────────────────────────────────────

func (r *Repository) buildListQuery(f asset.Filter) (string, []any) {
	where, args := r.buildWhereClause(f)
	query := "SELECT " + selectColumns + " FROM media_assets" + where + " ORDER BY created_at DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	if f.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, f.Offset)
	}
	return query, args
}

func (r *Repository) buildCountQuery(f asset.Filter) (string, []any) {
	where, _ := r.buildWhereClause(f)
	return "SELECT COUNT(*) FROM media_assets" + where, nil
}

func (r *Repository) buildWhereClause(f asset.Filter) (string, []any) {
	var conds []string
	var args []any

	// Always exclude soft-deleted unless explicitly requested
	hasDeleted := false
	for _, s := range f.States {
		if s == "deleted" {
			hasDeleted = true
		}
	}
	if !hasDeleted {
		conds = append(conds, "lifecycle_state != 'deleted'")
	}

	if f.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, f.Source)
	}
	if f.MediaType != "" {
		conds = append(conds, "media_type = ?")
		args = append(args, f.MediaType)
	}
	if len(f.IDs) > 0 {
		placeholders := make([]string, len(f.IDs))
		for i, id := range f.IDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conds = append(conds, "id IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(f.ExcludeIDs) > 0 {
		placeholders := make([]string, len(f.ExcludeIDs))
		for i, id := range f.ExcludeIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conds = append(conds, "id NOT IN ("+strings.Join(placeholders, ",")+")")
	}
	if f.IsFolder != nil {
		if *f.IsFolder {
			conds = append(conds, "is_folder = 1")
		} else {
			conds = append(conds, "is_folder = 0")
		}
	}
	if f.HasEmbedding != nil {
		if *f.HasEmbedding {
			conds = append(conds, "embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]'")
		} else {
			conds = append(conds, "(embedding_json IS NULL OR embedding_json = '' OR embedding_json = '[]')")
		}
	}

	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

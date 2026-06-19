// Package assetrepo provides the consolidated SQLite implementation of
// the domain asset repository interfaces. New code should import this
// package instead of internal/assets or internal/repository/clips.
//
// This package wraps the existing AssetStoreSQLite and exposes methods
// that satisfy the domain/asset.Repository, LocationRepository,
// ProcessingRepository, and VersionRepository interfaces.
package assetrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
	"go.uber.org/zap"
)

// Repository is the consolidated SQLite implementation of all asset
// domain repository interfaces. It wraps the existing AssetStoreSQLite
// and provides the canonical domain.Repository contract.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// New creates a new consolidated asset repository.
func New(db *sql.DB, log *zap.Logger) *Repository {
	if log == nil {
		log = zap.NewNop()
	}
	return &Repository{db: db, log: log}
}

// DB returns the underlying sql.DB for advanced use cases.
func (r *Repository) DB() *sql.DB { return r.db }

// Log returns the logger.
func (r *Repository) Log() *zap.Logger { return r.log }

// ── domain.Repository ───────────────────────────────────────────────

func (r *Repository) Upsert(ctx context.Context, a *asset.Asset) error {
	return r.upsertAsset(ctx, a)
}

func (r *Repository) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return r.getAssetByID(ctx, id)
}

func (r *Repository) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	return r.listAssetsByFilter(ctx, filter)
}

func (r *Repository) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	conds := []string{softDeleteFilter()}
	args := []any{}

	if filter.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.MediaType != "" {
		conds = append(conds, "media_type = ?")
		args = append(args, filter.MediaType)
	}
	if len(filter.States) > 0 {
		phs := make([]string, len(filter.States))
		for i := range filter.States {
			phs[i] = "?"
		}
		conds = append(conds, "lifecycle_state IN ("+strings.Join(phs, ",")+")")
		for _, st := range filter.States {
			args = append(args, st)
		}
	}

	var n int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE "+strings.Join(conds, " AND "),
		args...).Scan(&n)
	return n, err
}

func (r *Repository) SoftDelete(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		"UPDATE media_assets SET lifecycle_state = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ?",
		nowStr, nowStr, id)
	return err
}

func (r *Repository) Restore(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		"UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = NULL, updated_at = ? WHERE id = ?",
		nowStr, id)
	return err
}

func (r *Repository) HardDelete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_locations WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_processing WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_versions WHERE asset_id = ?", id)
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// ── UpsertTx (transactional, used by outbox dispatcher) ─────────────

// UpsertTx performs an upsert within an existing transaction.
func (r *Repository) UpsertTx(ctx context.Context, tx *sql.Tx, a *asset.Asset) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	tagsJSON, _ := jsonMarshal(a.Tags)
	searchTermsJSON, _ := jsonMarshal(a.SearchTerms)
	metadataJSON, _ := jsonMarshal(a.Metadata)
	deletedAtStr := ""
	if a.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*a.DeletedAt)
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, duration_ms, tags, search_terms,
			search_text, lifecycle_state, deleted_at, metadata_json,
			created_at, updated_at, folder_id, parent_folder_id, folder_path,
			scene_type, phash, last_used_at, quality_score, reuse_count,
			embedding_json, visual_embedding, transcript_embedding
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source, name = excluded.name, filename = excluded.filename,
			media_type = excluded.media_type, category = excluded.category, group_name = excluded.group_name,
			url = excluded.url, clip_page_url = excluded.clip_page_url, thumbnail_url = excluded.thumbnail_url,
			duration_ms = excluded.duration_ms, tags = excluded.tags, search_terms = excluded.search_terms,
			search_text = excluded.search_text, lifecycle_state = excluded.lifecycle_state,
			deleted_at = excluded.deleted_at, metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at, folder_id = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id, folder_path = excluded.folder_path,
			scene_type = excluded.scene_type, phash = excluded.phash,
			last_used_at = excluded.last_used_at, quality_score = excluded.quality_score,
			reuse_count = excluded.reuse_count, embedding_json = excluded.embedding_json,
			visual_embedding = excluded.visual_embedding, transcript_embedding = excluded.transcript_embedding
	`,
		a.ID, string(a.Source), a.Name, a.Filename, string(a.MediaType), a.Category, a.Group,
		a.SourceURL, a.ClipPageURL, a.ThumbnailURL, a.Duration.Milliseconds(),
		string(tagsJSON), string(searchTermsJSON),
		a.SearchText, string(a.LifecycleState), deletedAtStr, string(metadataJSON),
		timeutil.FormatRFC3339(a.CreatedAt), nowStr,
		a.FolderID(), a.ParentFolderID(), a.FolderPath(),
		a.SceneType(), a.PHash(), a.LastUsedAt(), a.QualityScore(), a.ReuseCount(),
		a.EmbeddingJSON(), a.VisualEmbedding(), a.TranscriptEmbedding(),
	)
	return err
}

// ── Internal helpers ────────────────────────────────────────────────

func (r *Repository) getAssetByID(ctx context.Context, id string) (*asset.Asset, error) {
	query := buildMediaAssetQuery("") + " AND id = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, id)
	return scanAsset(row)
}

func (r *Repository) upsertAsset(ctx context.Context, a *asset.Asset) error {
	if a == nil || a.ID == "" {
		return fmt.Errorf("assetrepo.Upsert: nil asset or empty ID")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	tagsJSON, _ := jsonMarshal(a.Tags)
	searchTermsJSON, _ := jsonMarshal(a.SearchTerms)
	metadataJSON := a.MetadataJSON()

	deletedAtStr := ""
	if a.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*a.DeletedAt)
	}
	createdAtStr := nowStr
	if !a.CreatedAt.IsZero() {
		createdAtStr = timeutil.FormatRFC3339(a.CreatedAt)
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type, category, group_name,
			url, clip_page_url, thumbnail_url, duration_ms, tags, search_terms,
			search_text, lifecycle_state, deleted_at, metadata_json,
			created_at, updated_at, folder_id, parent_folder_id, folder_path,
			scene_type, phash, last_used_at, quality_score, reuse_count,
			embedding_json, visual_embedding, transcript_embedding
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source, name = excluded.name, filename = excluded.filename,
			media_type = excluded.media_type, category = excluded.category, group_name = excluded.group_name,
			url = excluded.url, clip_page_url = excluded.clip_page_url, thumbnail_url = excluded.thumbnail_url,
			duration_ms = excluded.duration_ms, tags = excluded.tags, search_terms = excluded.search_terms,
			search_text = excluded.search_text, lifecycle_state = excluded.lifecycle_state,
			deleted_at = excluded.deleted_at, metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at, folder_id = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id, folder_path = excluded.folder_path,
			scene_type = excluded.scene_type, phash = excluded.phash,
			last_used_at = excluded.last_used_at, quality_score = excluded.quality_score,
			reuse_count = excluded.reuse_count, embedding_json = excluded.embedding_json,
			visual_embedding = excluded.visual_embedding, transcript_embedding = excluded.transcript_embedding
	`,
		a.ID, string(a.Source), a.Name, a.Filename, string(a.MediaType), a.Category, a.Group,
		a.SourceURL, a.ClipPageURL, a.ThumbnailURL, a.Duration.Milliseconds(),
		string(tagsJSON), string(searchTermsJSON),
		a.SearchText, string(a.LifecycleState), deletedAtStr, metadataJSON,
		createdAtStr, nowStr,
		a.FolderID(), a.ParentFolderID(), a.FolderPath(),
		a.SceneType(), a.PHash(), a.LastUsedAt(), a.QualityScore(), a.ReuseCount(),
		a.EmbeddingJSON(), a.VisualEmbedding(), a.TranscriptEmbedding(),
	)
	if err != nil {
		return fmt.Errorf("assetrepo.Upsert: %w", err)
	}
	return nil
}

func (r *Repository) listAssetsByFilter(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	cond := softDeleteFilter()
	args := []any{}

	if filter.Source != "" {
		cond += " AND source = ?"
		args = append(args, filter.Source)
	}
	if filter.MediaType != "" {
		cond += " AND media_type = ?"
		args = append(args, filter.MediaType)
	}
	if len(filter.States) > 0 {
		phs := make([]string, len(filter.States))
		for i := range filter.States {
			phs[i] = "?"
		}
		cond += " AND lifecycle_state IN (" + strings.Join(phs, ",") + ")"
		for _, st := range filter.States {
			args = append(args, st)
		}
	}
	if len(filter.IDs) > 0 {
		phs := make([]string, len(filter.IDs))
		for i := range filter.IDs {
			phs[i] = "?"
		}
		cond += " AND id IN (" + strings.Join(phs, ",") + ")"
		for _, id := range filter.IDs {
			args = append(args, id)
		}
	}
	if len(filter.ExcludeIDs) > 0 {
		phs := make([]string, len(filter.ExcludeIDs))
		for i := range filter.ExcludeIDs {
			phs[i] = "?"
		}
		cond += " AND id NOT IN (" + strings.Join(phs, ",") + ")"
		for _, id := range filter.ExcludeIDs {
			args = append(args, id)
		}
	}
	if filter.IsFolder != nil {
		cond += " AND is_folder = ?"
		v := 0
		if *filter.IsFolder {
			v = 1
		}
		args = append(args, v)
	}
	if filter.Category != "" {
		cond += " AND category = ?"
		args = append(args, filter.Category)
	}
	if filter.Group != "" {
		cond += " AND group_name = ?"
		args = append(args, filter.Group)
	}

	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE " + cond + " ORDER BY created_at DESC"
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

	var out []*asset.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("assetrepo.List scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── SQL helpers ─────────────────────────────────────────────────────

func softDeleteFilter() string {
	return "lifecycle_state != 'deleted'"
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

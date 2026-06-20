package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

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

func (r *ClipsRepository) Restore(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, "UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = NULL, updated_at = ? WHERE id = ?", nowStr, id)
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

func (r *ClipsRepository) UpsertClip(ctx context.Context, clip *asset.Asset) error {
	return r.Upsert(ctx, clip)
}

func (r *ClipsRepository) GetByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	return r.GetClipByDriveFileID(ctx, fileID)
}

func (r *ClipsRepository) GetClipFolderByVideoID(ctx context.Context, videoID string) (*media.ClipFolder, error) {
	return r.GetFolderByVideoID(ctx, videoID)
}

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
			embedding_json, visual_embedding, transcript_embedding
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			transcript_embedding = excluded.transcript_embedding
	`,
		clip.ID, string(clip.Source), clip.Name, clip.Filename, string(clip.MediaType), clip.Category, clip.Group,
		clip.SourceURL, clip.ClipPageURL, clip.ThumbnailURL, clip.Duration.Milliseconds(), string(tagsJSON), string(searchTermsJSON),
		clip.SearchText, string(clip.LifecycleState), deletedAtStr, string(metadataJSON),
		timeutil.FormatRFC3339(clip.CreatedAt), nowStr, clip.FolderID(), clip.ParentFolderID(), clip.FolderPath(),
		clip.SceneType(), clip.PHash(), clip.LastUsedAt(), clip.QualityScore(), clip.ReuseCount(),
		clip.EmbeddingJSON(), clip.VisualEmbedding(), clip.TranscriptEmbedding(),
	)
	return err
}

func (r *ClipsRepository) DeleteClip(ctx context.Context, id string) error {
	return r.SoftDelete(ctx, id)
}

func (r *ClipsRepository) RestoreClip(ctx context.Context, id string) error {
	return r.Restore(ctx, id)
}

func (r *ClipsRepository) HardDeleteClip(ctx context.Context, id string) error {
	return r.HardDelete(ctx, id)
}

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

func (r *ClipsRepository) CountAll(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+r.SoftDeleteFilter()).Scan(&n)
	return n, err
}

func (r *ClipsRepository) CountIndexed(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]'`).Scan(&n)
	return n, err
}

func (r *ClipsRepository) ListIndexedIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM media_assets WHERE `+r.SoftDeleteFilter()+`
		   AND embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *ClipsRepository) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
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

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clips.List: %w", err)
	}
	defer rows.Close()

	var out []*asset.Asset
	for rows.Next() {
		m, err := asset.ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, fmt.Errorf("clips.List scan: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

func (r *ClipsRepository) UpsertFolder(ctx context.Context, folder *media.ClipFolder) error {
	mBytes, err := json.Marshal(folder)
	if err != nil {
		return err
	}
	var assetsFolder asset.ClipFolder
	if err := json.Unmarshal(mBytes, &assetsFolder); err != nil {
		return err
	}
	return r.AssetStoreSQLite.UpsertFolder(ctx, &assetsFolder)
}

func (r *ClipsRepository) GetFolder(ctx context.Context, folderID string) (*media.ClipFolder, error) {
	folder, err := r.AssetStoreSQLite.GetFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, nil
	}
	mBytes, _ := json.Marshal(folder)
	var mFolder media.ClipFolder
	_ = json.Unmarshal(mBytes, &mFolder)
	return &mFolder, nil
}

func (r *ClipsRepository) GetFolderByVideoID(ctx context.Context, videoID string) (*media.ClipFolder, error) {
	folder, err := r.AssetStoreSQLite.GetFolderByVideoID(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, nil
	}
	mBytes, _ := json.Marshal(folder)
	var mFolder media.ClipFolder
	_ = json.Unmarshal(mBytes, &mFolder)
	return &mFolder, nil
}

func (r *ClipsRepository) GetFolderByPath(ctx context.Context, folderPath string) (*media.ClipFolder, error) {
	query := "SELECT id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at FROM clip_folders WHERE folder_path = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, folderPath)
	var folder media.ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
	folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &folder, nil
}

func (r *ClipsRepository) ListFolders(ctx context.Context, source string) ([]*media.ClipFolder, error) {
	folders, err := r.AssetStoreSQLite.ListFolders(ctx, source)
	if err != nil {
		return nil, err
	}
	out := make([]*media.ClipFolder, len(folders))
	for i, f := range folders {
		mBytes, _ := json.Marshal(f)
		var mFolder media.ClipFolder
		_ = json.Unmarshal(mBytes, &mFolder)
		out[i] = &mFolder
	}
	return out, nil
}

func inClause(n int, col string, notOpt ...string) string {
	if n <= 0 {
		return "1=1"
	}
	op := "IN"
	if len(notOpt) > 0 && strings.EqualFold(notOpt[0], "NOT") {
		op = "NOT IN"
	}
	placeholders := make([]string, n)
	for i := 0; i < n; i++ {
		placeholders[i] = "?"
	}
	return col + " " + op + " (" + strings.Join(placeholders, ",") + ")"
}

type AdvancedSearchRequest = asset.AdvancedSearchRequest
type AdvancedSearchResult = asset.AdvancedSearchResult
type SegmentEmbeddingRecord = asset.SegmentEmbeddingRecord

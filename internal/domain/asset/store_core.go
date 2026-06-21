package asset

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Store interface implementation ───────────────────────────────────────

// Get retrieves an asset's full details by ID, including locations,
// processing records, and version history. Returns nil, nil when the
// asset does not exist (not an error for callers that tolerate lookups).
func (s *AssetStoreSQLite) Get(ctx context.Context, id string) (*Details, error) {
	asset, err := getAssetByID(s.db, ctx, id)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, nil
	}

	locations, _ := s.ListLocationsByAsset(ctx, id)
	processingRecs, _ := s.GetProcessingRecordsByAssetID(ctx, id)
	versions, _ := s.ListVersions(ctx, id)

	processingPtrs := make([]*ProcessingRecord, len(processingRecs))
	for i := range processingRecs {
		processingPtrs[i] = &processingRecs[i]
	}
	versionPtrs := make([]*Version, len(versions))
	for i := range versions {
		versionPtrs[i] = &versions[i]
	}

	return &Details{
		Asset:      asset,
		Locations:  locations,
		Processing: processingPtrs,
		Versions:   versionPtrs,
	}, nil
}

// getAssetByID fetches a single non-deleted asset by primary key.
func getAssetByID(db *sql.DB, ctx context.Context, id string) (*Asset, error) {
	query := buildMediaAssetQuery("") + " AND id = ? LIMIT 1"
	row := db.QueryRowContext(ctx, query, id)
	return scanMediaAsset(row)
}

// List returns asset summaries matching the supplied filter.
func (s *AssetStoreSQLite) List(ctx context.Context, filter Filter) ([]*Summary, error) {
	query, args := buildSummaryQuery(filter)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assets.List: %w", err)
	}
	defer rows.Close()

	var out []*Summary
	for rows.Next() {
		sum, err := scanSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("assets.List scan: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// buildSummaryQuery constructs a SELECT query for the Summary type from
// the supplied Filter. It always includes the soft-delete filter.
func buildSummaryQuery(filter Filter) (string, []any) {
	args := []any{}
	conds := []string{SoftDeleteFilter()}

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
	if len(filter.IDs) > 0 {
		phs := make([]string, len(filter.IDs))
		for i := range filter.IDs {
			phs[i] = "?"
		}
		conds = append(conds, "id IN ("+strings.Join(phs, ",")+")")
		for _, id := range filter.IDs {
			args = append(args, id)
		}
	}
	if len(filter.ExcludeIDs) > 0 {
		phs := make([]string, len(filter.ExcludeIDs))
		for i := range filter.ExcludeIDs {
			phs[i] = "?"
		}
		conds = append(conds, "id NOT IN ("+strings.Join(phs, ",")+")")
		for _, id := range filter.ExcludeIDs {
			args = append(args, id)
		}
	}
	if filter.IsFolder != nil {
		conds = append(conds, "is_folder = ?")
		v := 0
		if *filter.IsFolder {
			v = 1
		}
		args = append(args, v)
	}
	if filter.Category != "" {
		conds = append(conds, "category = ?")
		args = append(args, filter.Category)
	}
	if filter.Group != "" {
		conds = append(conds, "group_name = ?")
		args = append(args, filter.Group)
	}

	query := "SELECT id, COALESCE(source,''), COALESCE(name,''), COALESCE(filename,''), " +
		"COALESCE(media_type,''), COALESCE(category,''), COALESCE(lifecycle_state,'ready'), " +
		"created_at, COALESCE(updated_at,'') " +
		"FROM media_assets WHERE " + strings.Join(conds, " AND ") +
		" ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	}
	return query, args
}

// scanSummary scans a single Summary row. Column order must match
// buildSummaryQuery exactly.
func scanSummary(scanner interface{ Scan(dest ...any) error }) (*Summary, error) {
	var sum Summary
	var sourceStr, nameStr, filenameStr, mediaTypeStr, categoryStr, lifecycleStr sql.NullString
	var createdAtStr, updatedAtStr sql.NullString
	err := scanner.Scan(
		&sum.ID, &sourceStr, &nameStr, &filenameStr,
		&mediaTypeStr, &categoryStr, &lifecycleStr,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}
	sum.Source = Source(sourceStr.String)
	sum.Name = nameStr.String
	sum.Filename = filenameStr.String
	sum.MediaType = MediaType(mediaTypeStr.String)
	sum.Category = categoryStr.String
	sum.LifecycleState = LifecycleState(lifecycleStr.String)
	if createdAtStr.Valid {
		sum.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	}
	if updatedAtStr.Valid {
		sum.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr.String)
	}
	return &sum, nil
}

// Save upserts an asset and its nested locations. It always overwrites
// on conflict (INSERT … ON CONFLICT DO UPDATE).
func (s *AssetStoreSQLite) Save(ctx context.Context, details *Details) error {
	if details == nil || details.Asset == nil {
		return fmt.Errorf("assets.Save: nil details or asset")
	}
	a := details.Asset

	if a.ID == "" {
		return fmt.Errorf("assets.Save: asset ID is required")
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	tagsJSON, _ := json.Marshal(a.Tags)
	searchTermsJSON, _ := json.Marshal(a.SearchTerms)
	metadataJSON := a.MetadataJSON()

	deletedAtStr := ""
	if a.DeletedAt != nil {
		deletedAtStr = timeutil.FormatRFC3339(*a.DeletedAt)
	}

	createdAtStr := nowStr
	if !a.CreatedAt.IsZero() {
		createdAtStr = timeutil.FormatRFC3339(a.CreatedAt)
	}

	_, err := s.db.ExecContext(ctx, `
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
		a.ID, string(a.Source), a.Name, a.Filename, string(a.MediaType), a.Category, a.Group,
		a.SourceURL, a.ClipPageURL, a.ThumbnailURL, a.Duration.Milliseconds(),
		string(tagsJSON), string(searchTermsJSON),
		a.SearchText, string(a.LifecycleState), deletedAtStr, metadataJSON,
		createdAtStr, nowStr,
		a.FolderID(), a.ParentFolderID(), a.FolderPath(),
		a.SceneType(), a.PHash(), a.LastUsedAt(), a.QualityScore(), a.ReuseCount(),
		a.EmbeddingJSON(), a.VisualEmbedding(), a.TranscriptEmbedding(),
		a.DriveLink(), a.DownloadLink(), a.LocalPath(), a.DriveFileID(), a.FileHash(),
	)
	if err != nil {
		return fmt.Errorf("assets.Save: %w", err)
	}

	// Persist nested locations when provided.
	if details.Locations != nil {
		for _, loc := range details.Locations {
			if loc == nil {
				continue
			}
			loc.AssetID = a.ID
			if err := s.UpsertLocation(ctx, loc); err != nil {
				return fmt.Errorf("assets.Save location: %w", err)
			}
		}
	}

	return nil
}

// Delete soft-deletes the asset by moving its lifecycle_state to
// 'deleted' and setting deleted_at.
func (s *AssetStoreSQLite) Delete(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx,
		"UPDATE media_assets SET lifecycle_state = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ?",
		nowStr, nowStr, id)
	return err
}

// ── Repository adapter ───────────────────────────────────────────────────

type assetRepositoryAdapter struct {
	store *AssetStoreSQLite
}

func (a *assetRepositoryAdapter) Upsert(ctx context.Context, asset *Asset) error {
	return a.store.Save(ctx, &Details{Asset: asset})
}

func (a *assetRepositoryAdapter) Get(ctx context.Context, id string) (*Asset, error) {
	det, err := a.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if det == nil {
		return nil, nil
	}
	return det.Asset, nil
}

func (a *assetRepositoryAdapter) List(ctx context.Context, filter Filter) ([]*Asset, error) {
	// Delegate to the same query path used by ListClips → ListClipsPaged.
	return a.store.listAssetsByFilter(ctx, filter)
}

func (a *assetRepositoryAdapter) Count(ctx context.Context, filter Filter) (int64, error) {
	conds := []string{SoftDeleteFilter()}
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
	err := a.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE "+strings.Join(conds, " AND "),
		args...).Scan(&n)
	return n, err
}

func (a *assetRepositoryAdapter) SoftDelete(ctx context.Context, id string) error {
	return a.store.Delete(ctx, id)
}

func (a *assetRepositoryAdapter) Restore(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := a.store.db.ExecContext(ctx,
		"UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = NULL, updated_at = ? WHERE id = ?",
		nowStr, id)
	return err
}

func (a *assetRepositoryAdapter) HardDelete(ctx context.Context, id string) error {
	tx, err := a.store.db.BeginTx(ctx, nil)
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

// AssetRepository returns a Repository adapter backed by this store.
func (s *AssetStoreSQLite) AssetRepository() Repository {
	return &assetRepositoryAdapter{store: s}
}

// listAssetsByFilter is a helper used internally by the adapter
// to return []*Asset matching a Filter. It reuses the shared media asset
// query builder and scanner.
func (s *AssetStoreSQLite) listAssetsByFilter(ctx context.Context, filter Filter) ([]*Asset, error) {
	cond := SoftDeleteFilter()
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

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assets.listAssetsByFilter: %w", err)
	}
	defer rows.Close()

	out := make([]*Asset, 0)
	for rows.Next() {
		a, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, fmt.Errorf("assets.listAssetsByFilter scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

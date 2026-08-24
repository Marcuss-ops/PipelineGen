// Package assets — canonical AssetStoreSQLite struct + minimal
// canonical Get / Save / Delete / List methods.
//
// Wave A / Blocco 1 / PR 1 Asset SSOT (June 2026): created here.
//
// HYBRID EMBED strategy (validated by prior thinker, June 2026):
//
// On top of the embed, LOCAL canonical methods (Get / Save / Delete /
// List) shadow the same-named receivers on the embedded legacy struct
// so callers using `r.AssetStoreSQLite.Save(...)` etc. always hit the
// new canonical impl. Public composition paths (line 39-40 of
// build_bundles_core.go) route through sqassets.NewAssetStoreSQLite +
// sqassets.NewService so the asset.NewAssetStoreSQLite back-compat
// shim is NOT retained (per user guidance: back-compat would cause
// domain→infra circular import).
package imagesregistry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// ── AssetStoreSQLite (canonical Wave A receiver) ────────────────────

type AssetStoreSQLite struct {
	db            *sql.DB
	log           *zap.Logger
	canonicalSave func(context.Context, *asset.Details) error
}

func (s *AssetStoreSQLite) SetCanonicalSave(fn func(context.Context, *asset.Details) error) {
	if s != nil {
		s.canonicalSave = fn
	}
}

// NewAssetStoreSQLite creates a new Wave A AssetStoreSQLite with
// the given database connection and logger (nil-safe). The legacy
// domain struct (with its 71 receivers) is constructed and embedded
// so callers can reach UpsertFolder / GetFolder / SearchClips /
// Locate / Process / Version / SegmentEmbedding
// receivers via promotion without per-receiver migration.
func NewAssetStoreSQLite(db *sql.DB, log *zap.Logger) *AssetStoreSQLite {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetStoreSQLite{db: db, log: log}
}

// ── canonical Get / Save / Delete / List (overlay) ──────────────────
//
// Each method shadows the same-named receiver on the embedded
// legacy struct so callers using r.AssetStoreSQLite.Save(...) etc.
// hit the canonical local impl. The legacy receivers are still
// reachable if explicitly invoked via the embedded field.

// Get retrieves a non-tombstoned asset by id, populated via the
// canonical MediaAssetColumns projection in store_helpers.go and the
// canonical ScanCanonicalAssetRowPublic in scan_helpers.go.
//
// Returns (nil, nil) when the asset does not exist (callers tolerate
// lookups and treat (nil,nil) as "not found").
func (s *AssetStoreSQLite) Get(ctx context.Context, id string) (*asset.Details, error) {
	if id == "" {
		return nil, nil
	}
	query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter() + " AND id = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, id)
	a, err := ScanCanonicalAssetRowPublic(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assets.Get: %w", err)
	}
	return &asset.Details{Asset: a}, nil
}

// Save upserts an asset (canonical INSERT ... ON CONFLICT DO UPDATE
// pattern). The SQL column projection matches UpsertClipTx in
// clips_repository.go (QDRANT-002 outbox-driver path) for
// consistency.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. Production callers
// that need vector indexing MUST use outbox.Dispatcher
// .EnqueueAndIndex (single tx UPSERT + outbox_events INSERT).
//
// Exempt callers (diagnostic-only, no indexing needed) match the
// legacy Save: clip_ops.go::verifyClip, deep_cleanup.go.
// MarkUsed is analytical-only and routed through its own method.
func (s *AssetStoreSQLite) Save(ctx context.Context, details *asset.Details) error {
	if details == nil || details.Asset == nil {
		return fmt.Errorf("assets.Save: nil details or asset")
	}
	a := details.Asset
	if a.ID == "" {
		return fmt.Errorf("assets.Save: asset ID is required")
	}
	if s.canonicalSave != nil {
		return s.canonicalSave(ctx, details)
	}

	nowStr := timeutil.FormatRFC3339(time.Now())
	a.SyncTagFieldsToMetadata()
	tagsJSON, _ := json.Marshal(a.Tags)
	searchTermsJSON, _ := json.Marshal(a.SearchTerms)
	metadataJSON, _ := json.Marshal(a.Metadata)
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
			drive_link, download_link, local_path, drive_file_id, legacy_file_md5
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
			legacy_file_md5 = excluded.legacy_file_md5
	`,
		a.ID, string(a.Source), a.Name, a.Filename, string(a.MediaType), a.Category, a.Group,
		a.SourceURL, a.ClipPageURL, a.ThumbnailURL, a.Duration.Milliseconds(),
		string(tagsJSON), string(searchTermsJSON),
		a.SearchText, string(a.LifecycleState), deletedAtStr, string(metadataJSON),
		createdAtStr, nowStr,
		a.FolderID(), a.ParentFolderID(), a.FolderPath(),
		a.SceneType(), a.PHash(), a.LastUsedAt(), a.QualityScore(), a.ReuseCount(),
		a.EmbeddingJSON(), a.VisualEmbedding(), a.TranscriptEmbedding(),
		a.DriveLink(), a.DownloadLink(), a.LocalPath(), a.DriveFileID(), a.LegacyFileMD5(),
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

// Delete soft-deletes the asset by flipping lifecycle_state to the
// canonical UPPERCASE 'DELETED' and stamping deleted_at + updated_at.
func (s *AssetStoreSQLite) Delete(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx,
		"UPDATE media_assets SET lifecycle_state = 'DELETED', deleted_at = ?, updated_at = ? WHERE id = ?",
		nowStr, nowStr, id)
	return err
}

// List returns canonical asset summaries matching the supplied
// filter. Implements the same projection as the legacy struct's
// List, ported verbatim into the canonical path.
func (s *AssetStoreSQLite) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
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

	query := "SELECT id, COALESCE(source,''), COALESCE(name,''), COALESCE(filename,''), " +
		"COALESCE(media_type,''), COALESCE(category,''), COALESCE(lifecycle_state,'ACTIVE'), " +
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

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assets.List: %w", err)
	}
	defer rows.Close()

	var out []*asset.Summary
	for rows.Next() {
		var sum asset.Summary
		var sourceStr, nameStr, filenameStr, mediaTypeStr, categoryStr, lifecycleStr sql.NullString
		var createdAtStr, updatedAtStr sql.NullString
		if err := rows.Scan(
			&sum.ID, &sourceStr, &nameStr, &filenameStr,
			&mediaTypeStr, &categoryStr, &lifecycleStr,
			&createdAtStr, &updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("assets.List scan: %w", err)
		}
		sum.Source = asset.Source(sourceStr.String)
		sum.Name = nameStr.String
		sum.Filename = filenameStr.String
		sum.MediaType = asset.MediaType(mediaTypeStr.String)
		sum.Category = categoryStr.String
		sum.LifecycleState = asset.LifecycleState(lifecycleStr.String)
		if createdAtStr.Valid {
			sum.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
		}
		if updatedAtStr.Valid {
			sum.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr.String)
		}
		out = append(out, &sum)
	}
	return out, rows.Err()
}

// ── Wave B NewService wrapper ──────────────────────────────────────

// NewService is the Wave B canonical surface for constructing the
// high-level asset Service. The Service type itself stays in the
// domain package (it's a pure orchestration wrapper around Store),
// but construction routes through sqassets per the Wave B
// composition-root migration. This deliberate indirection keeps the
// import graph clean (domain → infra never inverts) and lets the
// composition root use one consistent prefix for asset-store +
// service construction.
func NewService(store *AssetStoreSQLite, log *zap.Logger) *asset.Service {
	if log == nil {
		log = zap.NewNop()
	}
	return asset.NewService(store, log)
}

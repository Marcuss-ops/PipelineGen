// Package assetrepo provides the SQLite implementation of asset.Repository.
//
// This repository reads from typed columns in media_assets, NOT from
// metadata_json for canonical fields. It returns asset.MediaAsset from
// core/domain/asset, never models.MediaAsset.
package assetrepo

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// selectColumns lists the columns selected for a full asset load.
// Canonical fields are read from typed columns; only provider-specific
// metadata comes from metadata_json.
const selectColumns = `
	id,
	COALESCE(source, '')          AS source,
	COALESCE(name, '')            AS name,
	COALESCE(filename, '')        AS filename,
	COALESCE(media_type, '')      AS media_type,
	COALESCE(category, '')        AS category,
	COALESCE(group_name, '')      AS grp,
	COALESCE(url, '')             AS source_url,
	COALESCE(clip_page_url, '')   AS clip_page_url,
	COALESCE(thumbnail_url, '')   AS thumbnail_url,
	COALESCE(external_url, '')    AS external_url,
	COALESCE(duration_ms, 0)      AS duration_ms,
	COALESCE(tags, '[]')          AS tags,
	COALESCE(search_terms, '[]')  AS search_terms,
	COALESCE(search_text, '')     AS search_text,
	COALESCE(lifecycle_state, 'ready') AS lifecycle_state,
	deleted_at,
	COALESCE(metadata_json, '{}') AS metadata_json,
	COALESCE(embedding_json, '')  AS embedding_json,
	COALESCE(visual_embedding, '')  AS visual_embedding,
	COALESCE(transcript_embedding, '') AS transcript_embedding,
	COALESCE(visual_embedding_json, '') AS visual_embedding_json,
	COALESCE(folder_id, '')       AS folder_id,
	COALESCE(parent_folder_id, '') AS parent_folder_id,
	COALESCE(folder_path, '')     AS folder_path,
	COALESCE(depth, 0)            AS depth,
	is_folder,
	COALESCE(scene_type, '')      AS scene_type,
	COALESCE(quality_score, 0)    AS quality_score,
	COALESCE(reuse_count, 0)      AS reuse_count,
	COALESCE(last_used_at, '')    AS last_used_at,
	COALESCE(usable_for, '[]')    AS usable_for,
	COALESCE(avoid_for, '[]')     AS avoid_for,
	COALESCE(phash, '')           AS phash,
	COALESCE(child_count, 0)      AS child_count,
	COALESCE(status, '')          AS status,
	COALESCE(error, '')           AS err_msg,
	created_at,
	updated_at
`

type scanner interface {
	Scan(dest ...any) error
}

// scanAsset scans a single asset.MediaAsset from any SQL scanner.
func scanAsset(s scanner) (*asset.MediaAsset, error) {
	var a asset.MediaAsset
	var tagsJSON, searchTermsJSON, usableForJSON, avoidForJSON sql.NullString
	var metadataStr sql.NullString
	var embeddingJSON, visualEmb, transcriptEmb, visualEmbJSON sql.NullString
	var grp sql.NullString
	var createdAtStr, updatedAtStr, deletedAtStr sql.NullString
	var lifecycleStr sql.NullString
	var duration sql.NullInt64
	var depth, reuseCount, childCount sql.NullInt64
	var qualityScore sql.NullFloat64
	var isFolder sql.NullInt64

	err := s.Scan(
		&a.ID, &a.Source, &a.Name, &a.Filename, &a.MediaType, &a.Category,
		&grp, &a.SourceURL, &a.ClipPageURL, &a.ThumbnailURL, &a.ExternalURL,
		&duration,
		&tagsJSON, &searchTermsJSON, &a.SearchText,
		&lifecycleStr, &deletedAtStr,
		&metadataStr,
		&embeddingJSON, &visualEmb, &transcriptEmb, &visualEmbJSON,
		&a.FolderID, &a.ParentFolderID, &a.FolderPath, &depth, &isFolder,
		&a.SceneType, &qualityScore, &reuseCount, &a.LastUsedAt,
		&usableForJSON, &avoidForJSON, &a.PHash, &childCount,
		&a.Status, &a.Error,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}

	if grp.Valid {
		a.Group = grp.String
	}
	if duration.Valid {
		a.DurationMs = duration.Int64
	}
	if depth.Valid {
		a.Depth = int(depth.Int64)
	}
	if isFolder.Valid {
		a.IsFolder = isFolder.Int64 != 0
	}
	if qualityScore.Valid {
		a.QualityScore = qualityScore.Float64
	}
	if reuseCount.Valid {
		a.ReuseCount = int(reuseCount.Int64)
	}
	if childCount.Valid {
		a.ChildCount = int(childCount.Int64)
	}
	if lifecycleStr.Valid {
		a.LifecycleState = asset.LifecycleState(lifecycleStr.String)
	}

	if deletedAtStr.Valid && deletedAtStr.String != "" {
		if t := timeutil.ParseRFC3339(deletedAtStr.String); !t.IsZero() {
			a.DeletedAt = &t
		}
	}

	now := timeutil.ParseRFC3339(createdAtStr.String)
	if !now.IsZero() {
		a.CreatedAt = now
	}
	updated := timeutil.ParseRFC3339(updatedAtStr.String)
	if !updated.IsZero() {
		a.UpdatedAt = updated
	}

	if tagsJSON.Valid && tagsJSON.String != "" && tagsJSON.String != "[]" {
		_ = json.Unmarshal([]byte(tagsJSON.String), &a.Tags)
	}
	if searchTermsJSON.Valid && searchTermsJSON.String != "" && searchTermsJSON.String != "[]" {
		_ = json.Unmarshal([]byte(searchTermsJSON.String), &a.SearchTerms)
	}
	if usableForJSON.Valid && usableForJSON.String != "" && usableForJSON.String != "[]" {
		_ = json.Unmarshal([]byte(usableForJSON.String), &a.UsableFor)
	}
	if avoidForJSON.Valid && avoidForJSON.String != "" && avoidForJSON.String != "[]" {
		_ = json.Unmarshal([]byte(avoidForJSON.String), &a.AvoidFor)
	}

	if embeddingJSON.Valid {
		a.EmbeddingJSON = embeddingJSON.String
	}
	if visualEmb.Valid {
		a.VisualEmbedding = visualEmb.String
	}
	if transcriptEmb.Valid {
		a.TranscriptEmbedding = transcriptEmb.String
	}
	if visualEmbJSON.Valid {
		a.VisualEmbeddingJSON = visualEmbJSON.String
	}

	// Parse metadata_json — only for provider-specific fields
	a.SetMetadataJSON(metadataStr.String)

	return &a, nil
}

// normalizeTags converts a tag list to a lowercase, accent-stripped string.
func normalizeTags(tags []string) string {
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

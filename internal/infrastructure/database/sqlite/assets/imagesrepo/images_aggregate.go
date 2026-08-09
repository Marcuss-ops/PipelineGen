// Package assets — image aggregate listing operations.
//
// images_aggregate.go owns the FASE 6 canonical ListImages method
// (the routing.RepositoryListFilter-based replacement for
// ListImagesBySubject). Extracted from images_repository.go
// (July 2026, LONG-FILES-SPLIT-2026-07-06).
package imagesrepo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// FASE 6 (July 2026, image-territories action plan): ListImages is the
// FASE 6 canonical replacement for ListImagesBySubject. Takes a
// routing.RepositoryListFilter and returns routing.RepositoryImageRow
// rows (FASE 8: renamed from routing.ImageFilter/routing.ImageSearchResult
// to disambiguate from the canonical routing-layer DTOs that the
// ImageSearcher interface returns — the SQLite adapter needs the
// underlying Subject/Slug/Description columns to populate the join
// projection, so the read-model shape carries extra fields).
// See deprecation record PR-IMAGE-LISTIMAGESBYSUBJECT for the migration
// narrative; the physical removal of ListImagesBySubject is queued at
// the Wave 14 mega-package split gate, NOT in this commit.
//
// Implementation: routes against media_assets LEFT OUTER JOIN
// generated_image_details (gid) so StyleID/StyleVersion are populated
// for generated rows. Filter.Origins filtering hard-filters by
// media_assets.origin — when called from the generated-territory
// searcher (searcher_generated.go), Origins is pre-narrowed to
// [OriginGenerated], so no retrieved rows leak into the result.
func (r *ImagesRepository) ListImages(ctx context.Context, filter routing.RepositoryListFilter) ([]routing.RepositoryImageRow, error) {
	if r == nil {
		return nil, nil
	}
	limit := filter.Limit
	if limit <= 0 || limit > routing.MaxListImagesLimit {
		if limit > routing.MaxListImagesLimit {
			limit = routing.MaxListImagesLimit
		} else {
			limit = routing.DefaultResolvedLimit
		}
	}

	var sb strings.Builder
	sb.WriteString(`SELECT ma.id, ma.origin, ma.provider, json_extract(ma.metadata_json, '$.subject_id'), COALESCE(NULLIF(ma.url, ''), NULLIF(ma.thumbnail_url, ''), NULLIF(ma.thumb_url, ''), rid.source_image_url), ma.drive_link, ma.file_hash, ma.width, ma.height, rid.source_page_url, rid.license, rid.author, gid.prompt_resolved, gid.style_id, gid.style_version FROM media_assets ma LEFT JOIN retrieved_image_details rid ON ma.id = rid.asset_id LEFT JOIN generated_image_details gid ON ma.id = gid.asset_id WHERE 1=1`)
	args := []any{}

	if filter.SubjectID != "" {
		sb.WriteString(" AND json_extract(ma.metadata_json, '$.subject_id') = ?")
		args = append(args, filter.SubjectID)
	}

	if len(filter.Origins) > 0 {
		ph := make([]string, len(filter.Origins))
		for i, o := range filter.Origins {
			ph[i] = "?"
			args = append(args, string(o))
		}
		sb.WriteString(" AND ma.origin IN (" + strings.Join(ph, ",") + ")")
	}

	if len(filter.Providers) > 0 {
		ph := make([]string, len(filter.Providers))
		for i, p := range filter.Providers {
			ph[i] = "?"
			args = append(args, p)
		}
		sb.WriteString(" AND ma.provider IN (" + strings.Join(ph, ",") + ")")
	}

	if len(filter.StyleIDs) > 0 {
		ph := make([]string, len(filter.StyleIDs))
		for i, s := range filter.StyleIDs {
			ph[i] = "?"
			args = append(args, s)
		}
		sb.WriteString(" AND gid.style_id IN (" + strings.Join(ph, ",") + ")")
	}

	if len(filter.Tags) > 0 {
		for _, tag := range filter.Tags {
			sb.WriteString(" AND ma.tags LIKE ?")
			args = append(args, "%"+tag+"%")
		}
	}

	sb.WriteString(" ORDER BY ma.id DESC LIMIT ?")
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]routing.RepositoryImageRow, 0, limit)
	for rows.Next() {
		var (
			id, originStr, providerID, subjectID, previewURL, driveLink, fileHash sql.NullString
			width, height                                                         sql.NullInt64
			sourcePageURL, license, author                                        sql.NullString
			promptResolved, styleID, styleVersion                                 sql.NullString
		)
		err := rows.Scan(&id, &originStr, &providerID, &subjectID, &previewURL, &driveLink, &fileHash, &width, &height, &sourcePageURL, &license, &author, &promptResolved, &styleID, &styleVersion)
		if err != nil {
			return nil, err
		}
		name := ""
		if promptResolved.Valid {
			runes := []rune(promptResolved.String)
			if len(runes) > 80 {
				name = string(runes[:80])
			} else {
				name = promptResolved.String
			}
		}
		var w, h int
		if width.Valid {
			w = int(width.Int64)
		}
		if height.Valid {
			h = int(height.Int64)
		}
		out = append(out, routing.RepositoryImageRow{
			AssetID:       id.String,
			Origin:        asset.ImageOrigin(originStr.String),
			Provider:      providerID.String,
			Name:          name,
			PreviewURL:    previewURL.String,
			DriveLink:     driveLink.String,
			FileHash:      fileHash.String,
			SourcePageURL: sourcePageURL.String,
			License:       license.String,
			Author:        author.String,
			Width:         w,
			Height:        h,
			Score:         1.0,
			StyleID:       styleID.String,
			StyleVersion:  styleVersion.String,
		})
	}
	return out, rows.Err()
}

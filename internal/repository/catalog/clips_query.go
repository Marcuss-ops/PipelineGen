package catalog

import (
	"context"
	"database/sql"
	"strings"

	"velox/go-master/pkg/textutil"
)

// SearchClips queries the clips database for matching media.
func (r *Repository) SearchClips(q string) ([]CatalogRecord, error) {
	if r.clipsRepo == nil {
		return nil, nil
	}

	queryNorm := textutil.NormalizeQuery(q)

	clips, err := r.clipsRepo.SearchClipsByKeywords(context.Background(), strings.Split(q, " "), 100)
	if err != nil {
		// Fallback to simple LIKE search
		return r.searchClipsFallback(queryNorm)
	}

	var results []CatalogRecord
	for _, clip := range clips {
		rec := CatalogRecord{
			ID:        clip.ID,
			Name:      clip.Name,
			Path:      clip.FolderPath,
			Link:      clip.DriveLink,
			Source:    "clip_drive",
			DriveID:   clip.ID,
			MediaType: clip.MediaType,
			Tags:      clip.Tags,
			Duration:  clip.Duration,
		}
		results = append(results, rec)
	}
	return results, nil
}

// searchClipsFallback performs a simple LIKE search as fallback.
func (r *Repository) searchClipsFallback(queryNorm string) ([]CatalogRecord, error) {
	db := r.clipsRepo.DB()
	rows, err := db.Query(`
		SELECT id, name, folder_path, drive_link, media_type, tags, duration
		FROM clips
		WHERE LOWER(REPLACE(folder_path, ' ', '')) LIKE ?
		   OR LOWER(REPLACE(name, ' ', '')) LIKE ?
		LIMIT 100
	`, "%"+queryNorm+"%", "%"+queryNorm+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CatalogRecord
	for rows.Next() {
		var rec CatalogRecord
		var mediaType sql.NullString
		var tagsRaw sql.NullString
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Path, &rec.Link, &mediaType, &tagsRaw, &rec.Duration); err == nil {
			rec.Source = "clip_drive"
			rec.DriveID = rec.ID
			if mediaType.Valid {
				rec.MediaType = mediaType.String
			}
			if tagsRaw.Valid {
				rec.Tags = ParseTags(tagsRaw.String)
			}
			results = append(results, rec)
		}
	}
	return results, nil
}

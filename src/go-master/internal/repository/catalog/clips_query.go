package catalog

import (
	"database/sql"
	"path/filepath"

	"velox/go-master/pkg/textutil"
	_ "github.com/mattn/go-sqlite3"
)

// SearchClips queries the clips database for matching media.
func (r *Repository) SearchClips(q string) ([]CatalogRecord, error) {
	dbPath := filepath.Join(r.dataDir, "clips.db.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	queryNorm := textutil.NormalizeQuery(q)

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
		var mediaType, tagsRaw sql.NullString
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

package catalog

import (
	"database/sql"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// SearchArtlist queries the artlist database for matching folders.
func (r *Repository) SearchArtlist(q string) ([]CatalogRecord, error) {
	dbPath := filepath.Join(r.dataDir, "artlist.db.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT drive_id, name, full_path, drive_link 
		FROM artlist_folders 
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CatalogRecord
	for rows.Next() {
		var rec CatalogRecord
		if err := rows.Scan(&rec.DriveID, &rec.Name, &rec.Path, &rec.Link); err == nil {
			rec.Source = "artlist"
			rec.ID = rec.DriveID
			if strings.Contains(strings.ReplaceAll(strings.ToLower(rec.Name+rec.Path), " ", ""), q) {
				results = append(results, rec)
			}
		}
	}
	return results, nil
}

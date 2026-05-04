package catalog

import (
	"strings"
)

// SearchArtlist queries the artlist database for matching folders.
func (r *Repository) SearchArtlist(q string) ([]CatalogRecord, error) {
	if r.artlistRepo == nil {
		return nil, nil
	}

	db := r.artlistRepo.DB()
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

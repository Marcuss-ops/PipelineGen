package catalog

import (
	"context"
	"strings"
)

// SearchArtlist queries the artlist database for matching folders.
func (r *Repository) SearchArtlist(ctx context.Context, q string) ([]CatalogRecord, error) {
	if r.artlistRepo == nil {
		return nil, nil
	}

	db := r.artlistRepo.DB()
	// Normalize query: lowercase, remove spaces
	normalizedQ := strings.ReplaceAll(strings.ToLower(q), " ", "")
	rows, err := db.Query(`
		SELECT folder_id, group_name, folder_path, folder_id 
		FROM clip_folders 
		WHERE search_key LIKE ?
	`, "%"+normalizedQ+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CatalogRecord
	for rows.Next() {
		var rec CatalogRecord
		var folderID, groupName, folderPath string
		if err := rows.Scan(&folderID, &groupName, &folderPath, &rec.DriveID); err == nil {
			rec.Source = "artlist"
			rec.ID = folderID
			rec.Name = groupName
			rec.Path = folderPath
			rec.Link = "https://drive.google.com/drive/folders/" + folderID
			results = append(results, rec)
		}
	}
	return results, nil
}

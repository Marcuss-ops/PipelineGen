package catalog

import (
	"context"
	"path/filepath"
)

// SearchStock queries the stock database for matching folders/assets.
func (r *Repository) SearchStock(q string) ([]CatalogRecord, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	var results []CatalogRecord

	// 1. Try clip_folders table
	rows, err := db.Query(`
		SELECT folder_id, folder_path, group_name, source_url
		FROM clip_folders
		WHERE LOWER(COALESCE(folder_path, '')) LIKE ? OR LOWER(COALESCE(group_name, '')) LIKE ? OR LOWER(COALESCE(source_url, '')) LIKE ?
	`, "%"+q+"%", "%"+q+"%", "%"+q+"%")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var rec CatalogRecord
			if err := rows.Scan(&rec.DriveID, &rec.Path, &rec.Group, &rec.Link); err == nil {
				rec.Source = "stock_drive"
				rec.Name = filepath.Base(rec.Path)
				rec.ID = rec.DriveID
				results = append(results, rec)
			}
		}
	}

	// 2. Fallback to media_assets via repository
	if len(results) == 0 {
		clips, err := r.stockRepo.SearchClipsByKeywords(context.Background(), "stock", []string{q}, 100)
		if err == nil {
			for _, clip := range clips {
				if clip.Source == "stock" || clip.MediaType == "stock" {
					rec := CatalogRecord{
						ID:        clip.ID,
						Name:      clip.Name,
						Path:      clip.FolderPath,
						Link:      clip.DriveLink,
						Source:    clip.Source,
						DriveID:   clip.ID,
						MediaType: clip.MediaType,
						Tags:      clip.Tags,
						Duration:  clip.Duration,
					}
					results = append(results, rec)
				}
			}
		}
	}

	return results, nil
}

// LoadStockFolders loads all stock folders.
func (r *Repository) LoadStockFolders() ([]StockClipRef, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	rows, err := db.Query(`
		SELECT
			COALESCE(folder_id, ''),
			COALESCE(folder_path, ''),
			COALESCE(group_name, ''),
			COALESCE(source_url, '')
		FROM clip_folders
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []StockClipRef
	for rows.Next() {
		var folder StockClipRef
		if err := rows.Scan(&folder.FolderID, &folder.FullPath, &folder.TopicSlug, &folder.DriveLink); err != nil {
			return nil, err
		}
		folder.MediaType = "stock"
		folders = append(folders, folder)
	}
	return folders, nil
}

// LoadStockCatalog loads individual stock clips.
func (r *Repository) LoadStockCatalog() ([]StockClipRef, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	rows, err := db.Query(`
		SELECT
			COALESCE(id, ''),
			COALESCE(name, ''),
			COALESCE(json_extract(metadata_json, '$.filename'), ''),
			COALESCE(json_extract(metadata_json, '$.folder_id'), ''),
			COALESCE(json_extract(metadata_json, '$.folder_path'), ''),
			COALESCE(json_extract(metadata_json, '$.group_name'), ''),
			COALESCE(json_extract(metadata_json, '$.media_type'), 'stock'),
			COALESCE(json_extract(metadata_json, '$.drive_link'), ''),
			COALESCE(tags, ''),
			COALESCE(source, 'stock'),
			COALESCE(duration_ms / 1000, 0)
		FROM media_assets
		WHERE LOWER(COALESCE(source, 'stock')) = 'stock' OR LOWER(COALESCE(json_extract(metadata_json, '$.media_type'), '')) = 'stock'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []StockClipRef
	for rows.Next() {
		var clip StockClipRef
		var tagsRaw, source string
		if err := rows.Scan(
			&clip.ClipID,
			&clip.Name,
			&clip.Filename,
			&clip.FolderID,
			&clip.FolderPath,
			&clip.Group,
			&clip.MediaType,
			&clip.DriveLink,
			&tagsRaw,
			&source,
			&clip.Duration,
		); err != nil {
			return nil, err
		}
		if tagsRaw != "" {
			clip.Tags = ParseTags(tagsRaw)
		}
		if clip.MediaType == "" {
			clip.MediaType = "stock"
		}
		if clip.TopicSlug == "" {
			clip.TopicSlug = source
		}
		clips = append(clips, clip)
	}
	return clips, nil
}

package catalog

import (
	"context"
	"path/filepath"
)

// SearchStock queries the stock database (and clips as fallback) for matching folders/assets.
func (r *Repository) SearchStock(q string) ([]CatalogRecord, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	var results []CatalogRecord

	// 1. Try stock_folders table
	rows, err := db.Query(`
		SELECT drive_id, full_path, topic_slug, drive_link 
		FROM stock_folders 
		WHERE LOWER(full_path) LIKE ? OR LOWER(topic_slug) LIKE ?
	`, "%"+q+"%", "%"+q+"%")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var rec CatalogRecord
			if err := rows.Scan(&rec.DriveID, &rec.Path, &rec.TopicSlug, &rec.Link); err == nil {
				rec.Source = "stock_drive"
				rec.Name = filepath.Base(rec.Path)
				rec.ID = rec.DriveID
				results = append(results, rec)
			}
		}
	}

	// 2. Fallback to clips table using repository
	if len(results) == 0 {
		clips, err := r.stockRepo.SearchClipsByKeywords(context.Background(), []string{q}, 100)
		if err == nil {
			for _, clip := range clips {
				if clip.Source == "stock" || clip.MediaType == "stock" {
					rec := CatalogRecord{
						ID:        clip.ID,
						Name:       clip.Name,
						Path:       clip.FolderPath,
						Link:       clip.DriveLink,
						Source:     clip.Source,
						DriveID:    clip.ID,
						MediaType:  clip.MediaType,
						Tags:       clip.Tags,
						Duration:   clip.Duration,
					}
					results = append(results, rec)
				}
			}
		}
	}

	return results, nil
}

// LoadStockFolders loads all stock folders. Fallback to clips if needed.
func (r *Repository) LoadStockFolders() ([]StockClipRef, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	rows, err := db.Query(`
		SELECT
			COALESCE(drive_id, ''),
			COALESCE(full_path, ''),
			COALESCE(topic_slug, ''),
			COALESCE(drive_link, '')
		FROM stock_folders
	`)
	if err != nil {
		return r.loadStockFolderCatalogFromClipsTable()
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

func (r *Repository) loadStockFolderCatalogFromClipsTable() ([]StockClipRef, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	rows, err := db.Query(`
		SELECT
			COALESCE(id, ''),
			COALESCE(name, ''),
			COALESCE(filename, ''),
			COALESCE(folder_id, ''),
			COALESCE(folder_path, ''),
			COALESCE(group_name, ''),
			COALESCE(media_type, 'stock'),
			COALESCE(drive_link, ''),
			COALESCE(tags, ''),
			COALESCE(source, 'stock')
		FROM clips
		WHERE LOWER(COALESCE(source, 'stock')) = 'stock' OR LOWER(COALESCE(media_type, '')) = 'stock'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []StockClipRef
	for rows.Next() {
		var folder StockClipRef
		var tagsRaw, source string
		if err := rows.Scan(
			&folder.ClipID,
			&folder.Name,
			&folder.Filename,
			&folder.FolderID,
			&folder.FolderPath,
			&folder.Group,
			&folder.MediaType,
			&folder.DriveLink,
			&tagsRaw,
			&source,
		); err != nil {
			return nil, err
		}
		if tagsRaw != "" {
			folder.Tags = ParseTags(tagsRaw)
		}
		if folder.MediaType == "" {
			folder.MediaType = "stock"
		}
		if folder.TopicSlug == "" {
			folder.TopicSlug = source
		}
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
			COALESCE(clip_id, ''),
			COALESCE(filename, ''),
			COALESCE(topic_slug, ''),
			COALESCE(tags, ''),
			COALESCE(duration, 0)
		FROM stock_clips
	`)
	if err != nil {
		return r.loadStockCatalogFromClipsTable()
	}
	defer rows.Close()

	var clips []StockClipRef
	for rows.Next() {
		var clip StockClipRef
		var tagsStr string
		if err := rows.Scan(&clip.ClipID, &clip.Filename, &clip.TopicSlug, &tagsStr, &clip.Duration); err != nil {
			return nil, err
		}
		clip.Tags = ParseTags(tagsStr)
		clips = append(clips, clip)
	}
	return clips, nil
}

func (r *Repository) loadStockCatalogFromClipsTable() ([]StockClipRef, error) {
	if r.stockRepo == nil {
		return nil, nil
	}

	db := r.stockRepo.DB()
	rows, err := db.Query(`
		SELECT
			COALESCE(id, ''),
			COALESCE(name, ''),
			COALESCE(filename, ''),
			COALESCE(folder_id, ''),
			COALESCE(folder_path, ''),
			COALESCE(group_name, ''),
			COALESCE(media_type, 'stock'),
			COALESCE(drive_link, ''),
			COALESCE(tags, ''),
			COALESCE(source, 'stock'),
			COALESCE(duration, 0)
		FROM clips
		WHERE LOWER(COALESCE(source, 'stock')) = 'stock' OR LOWER(COALESCE(media_type, '')) = 'stock'
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

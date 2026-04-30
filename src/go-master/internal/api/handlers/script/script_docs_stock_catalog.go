package script

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func loadStockFolderCatalog(dataDir string) ([]stockClipRef, error) {
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "stock.db.sqlite"))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT
			COALESCE(drive_id, ''),
			COALESCE(full_path, ''),
			COALESCE(topic_slug, ''),
			COALESCE(drive_link, '')
		FROM stock_folders
	`)
	if err != nil {
		return loadStockFolderCatalogFromClipsTable(dataDir)
	}
	defer rows.Close()

	var folders []stockClipRef
	for rows.Next() {
		var folder stockClipRef
		if err := rows.Scan(&folder.FolderID, &folder.FullPath, &folder.TopicSlug, &folder.DriveLink); err != nil {
			return nil, err
		}
		folder.MediaType = "stock"
		folders = append(folders, folder)
	}
	return folders, nil
}

func loadStockFolderCatalogFromClipsTable(dataDir string) ([]stockClipRef, error) {
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "stock.db.sqlite"))
	if err != nil {
		return nil, err
	}
	defer db.Close()

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

	var folders []stockClipRef
	for rows.Next() {
		var folder stockClipRef
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
		if strings.TrimSpace(tagsRaw) != "" {
			if tags := splitCSV(tagsRaw); len(tags) > 0 {
				folder.Tags = tags
			} else {
				_ = json.Unmarshal([]byte(tagsRaw), &folder.Tags)
			}
		}
		if folder.MediaType == "" {
			folder.MediaType = "stock"
		}
		if folder.TopicSlug == "" {
			folder.TopicSlug = strings.TrimSpace(source)
		}
		folders = append(folders, folder)
	}
	return folders, nil
}

func loadStockFolderMatchIndex(dataDir string) (*stockFolderMatchIndex, error) {
	stockFolderIndexCache.mu.Lock()
	defer stockFolderIndexCache.mu.Unlock()

	if cached, ok := stockFolderIndexCache.data[dataDir]; ok {
		return cached, nil
	}

	path := filepath.Join(dataDir, "clipsearch_checkpoints.json")
	var index stockFolderMatchIndex
	if err := readJSON(path, &index); err != nil {
		folders, folderErr := loadStockFolderCatalog(dataDir)
		if folderErr != nil || len(folders) == 0 {
			return nil, err
		}
		index = buildStockFolderMatchIndexFromCatalog(folders)
	}

	stockFolderIndexCache.data[dataDir] = &index
	return &index, nil
}

func buildStockFolderMatchIndexFromCatalog(folders []stockClipRef) stockFolderMatchIndex {
	index := stockFolderMatchIndex{
		Records: make([]stockFolderMatchRecord, 0, len(folders)),
		DF:      make(map[string]int),
	}

	totalLen := 0
	for _, folder := range folders {
		normKey := normalizeMatchText(strings.Join([]string{
			folder.StockPath(),
			folder.TopicSlug,
			folder.Group,
			folder.DisplayName(),
			folder.FolderID,
			folder.DriveLink,
		}, " "))
		tokens := uniqueStrings(matchTokens(normKey))
		counts := make(map[string]int, len(tokens))
		for _, token := range tokens {
			counts[token]++
		}
		for token := range counts {
			index.DF[token]++
		}
		totalLen += len(tokens)
		index.Records = append(index.Records, stockFolderMatchRecord{
			Folder:  folder,
			NormKey: normKey,
			Tokens:  tokens,
			Counts:  counts,
			Length:  len(tokens),
		})
	}

	if len(index.Records) > 0 {
		index.AvgLen = float64(totalLen) / float64(len(index.Records))
	}

	return index
}

func loadStockCatalogFromSQLite(path string) ([]stockClipRef, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

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
		return loadStockCatalogFromClipsTable(path)
	}
	defer rows.Close()

	var clips []stockClipRef
	for rows.Next() {
		var clip stockClipRef
		var tagsStr string
		if err := rows.Scan(&clip.ClipID, &clip.Filename, &clip.TopicSlug, &tagsStr, &clip.Duration); err != nil {
			return nil, err
		}
		clip.Tags = splitCSV(tagsStr)
		clips = append(clips, clip)
	}
	return clips, nil
}

func loadStockCatalog(dataDir string) ([]stockClipRef, error) {
	return loadStockCatalogFromSQLite(filepath.Join(dataDir, "stock.db.sqlite"))
}

func loadStockCatalogFromClipsTable(path string) ([]stockClipRef, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

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

	var clips []stockClipRef
	for rows.Next() {
		var clip stockClipRef
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
		if strings.TrimSpace(tagsRaw) != "" {
			if tags := splitCSV(tagsRaw); len(tags) > 0 {
				clip.Tags = tags
			} else {
				_ = json.Unmarshal([]byte(tagsRaw), &clip.Tags)
			}
		}
		if clip.MediaType == "" {
			clip.MediaType = "stock"
		}
		if clip.TopicSlug == "" {
			clip.TopicSlug = strings.TrimSpace(source)
		}
		clips = append(clips, clip)
	}
	return clips, nil
}

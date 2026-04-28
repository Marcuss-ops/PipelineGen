package script

import (
	"database/sql"
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
			COALESCE(topic_slug, '')
		FROM stock_folders
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []stockClipRef
	for rows.Next() {
		var folder stockClipRef
		if err := rows.Scan(&folder.FolderID, &folder.FullPath, &folder.TopicSlug); err != nil {
			return nil, err
		}
		folder.MediaType = "stock"
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
		return nil, err
	}

	stockFolderIndexCache.data[dataDir] = &index
	return &index, nil
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
		return nil, err
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

func splitCSV(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

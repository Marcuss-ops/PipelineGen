package script

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func (s stockClipRef) DisplayName() string {
	if strings.TrimSpace(s.Name) != "" {
		return s.Name
	}
	if strings.TrimSpace(s.Filename) != "" {
		return s.Filename
	}
	return s.ClipID
}

func (s stockClipRef) PickLink() string {
	if strings.TrimSpace(s.FolderID) != "" {
		return "https://drive.google.com/drive/folders/" + s.FolderID
	}
	if strings.TrimSpace(s.DriveLink) != "" {
		return s.DriveLink
	}
	if strings.TrimSpace(s.ClipID) != "" {
		return "https://drive.google.com/file/d/" + s.ClipID + "/view?usp=drivesdk"
	}
	return ""
}

func (s stockClipRef) StockPath() string {
	base := strings.TrimSpace(s.FullPath)
	if base == "" {
		base = strings.TrimSpace(s.FolderPath)
	}
	if base == "" {
		base = strings.TrimSpace(s.Name)
	}
	if base == "" {
		base = strings.TrimSpace(s.Filename)
	}
	if base == "" {
		base = s.ClipID
	}
	if strings.HasPrefix(strings.ToLower(base), "stock/") {
		return normalizeStockPath(base, s.TopicSlug)
	}
	return normalizeStockPath(filepath.ToSlash(filepath.Join("Stock", base)), s.TopicSlug)
}

func normalizePathDisplay(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, pathComponentDisplay(part))
	}
	return strings.Join(out, "/")
}

func normalizeStockPath(path, topicSlug string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, pathComponentDisplay(part))
	}
	return strings.Join(out, "/")
}

func pathComponentDisplay(text string) string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == '.'
	})
	if len(parts) == 0 {
		return "Unknown"
	}
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		b.WriteString(strings.ToUpper(lower[:1]))
		if len(lower) > 1 {
			b.WriteString(lower[1:])
		}
		if b.Len() == 0 {
			return "Unknown"
		}
		return b.String()
	}
	return "Unknown"
}

func loadStockCatalog(dataDir string) ([]stockClipRef, error) {
	clips, err := loadStockCatalogFromSQLite(filepath.Join(dataDir, "stock.db.sqlite"))
	if err == nil && len(clips) > 0 {
		return clips, nil
	}
	return nil, fmt.Errorf("failed to load stock catalog from SQLite: %w", err)
}

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

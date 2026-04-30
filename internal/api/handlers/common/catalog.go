package common

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"velox/go-master/pkg/textutil"
)

type FolderResult struct {
	Source    string `json:"source"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Link      string `json:"link"`
	DriveID   string `json:"drive_id,omitempty"`
	TopicSlug string `json:"topic_slug,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type CatalogHandler struct {
	dataDir string
}

func NewCatalogHandler(dataDir string) *CatalogHandler {
	return &CatalogHandler{
		dataDir: dataDir,
	}
}

func (h *CatalogHandler) SearchFolders(c *gin.Context) {
	q := textutil.NormalizeQuery(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing query parameter q"})
		return
	}

	var results []FolderResult

	stockDBPath := filepath.Join(h.dataDir, "stock.db.sqlite")
	if stockResults, err := h.searchStockDB(stockDBPath, q); err == nil {
		results = append(results, stockResults...)
	}

	artlistDBPath := filepath.Join(h.dataDir, "artlist.db.sqlite")
	if artlistResults, err := h.searchArtlistDB(artlistDBPath, q); err == nil {
		results = append(results, artlistResults...)
	}

	clipsDBPath := filepath.Join(h.dataDir, "velox.db.sqlite")
	if clipsResults, err := h.searchClipsDB(clipsDBPath, q); err == nil {
		results = append(results, clipsResults...)
	}

	c.JSON(http.StatusOK, gin.H{
		"query":   q,
		"count":   len(results),
		"results": results,
	})
}

func (h *CatalogHandler) searchStockDB(dbPath string, q string) ([]FolderResult, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	var results []FolderResult
	rows, err := db.Query(`
		SELECT drive_id, full_path, topic_slug, drive_link 
		FROM stock_folders 
		WHERE LOWER(full_path) LIKE ? OR LOWER(topic_slug) LIKE ?
	`, "%"+q+"%", "%"+q+"%")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r FolderResult
			var fullPath, topicSlug string
			if err := rows.Scan(&r.DriveID, &fullPath, &topicSlug, &r.Link); err == nil {
				r.Source = "stock_drive"
				r.Path = fullPath
				r.Name = filepath.Base(fullPath)
				r.TopicSlug = topicSlug
				results = append(results, r)
			}
		}
	}

	if len(results) == 0 {
		clipRows, clipErr := db.Query(`
			SELECT id, name, folder_path, drive_link, media_type
			FROM clips
			WHERE (source = 'stock' OR media_type = 'stock')
			  AND (LOWER(REPLACE(folder_path, ' ', '')) LIKE ?
			   OR LOWER(REPLACE(name, ' ', '')) LIKE ?)
			LIMIT 100
		`, "%"+q+"%", "%"+q+"%")
		if clipErr == nil {
			defer clipRows.Close()
			for clipRows.Next() {
				var r FolderResult
				var id string
				var mediaType sql.NullString
				var name, folderPath string
				if err := clipRows.Scan(&id, &name, &folderPath, &r.Link, &mediaType); err == nil {
					r.Source = "stock_drive"
					r.DriveID = id
					r.Name = name
					r.Path = folderPath
					if mediaType.Valid {
						r.MediaType = mediaType.String
					}
					results = append(results, r)
				}
			}
		}
	}

	return results, nil
}

func (h *CatalogHandler) searchArtlistDB(dbPath string, q string) ([]FolderResult, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	rows, err := db.Query(`
		SELECT drive_id, name, full_path, drive_link 
		FROM artlist_folders 
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FolderResult
	for rows.Next() {
		var r FolderResult
		var name, fullPath string
		if err := rows.Scan(&r.DriveID, &name, &fullPath, &r.Link); err == nil {
			r.Source = "artlist"
			r.Name = name
			r.Path = fullPath
			if strings.Contains(strings.ReplaceAll(strings.ToLower(name+fullPath), " ", ""), q) {
				results = append(results, r)
			}
		}
	}
	return results, nil
}

func (h *CatalogHandler) searchClipsDB(dbPath string, q string) ([]FolderResult, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	queryNorm := normalizeQueryFromDB(q)

	rows, err := db.Query(`
		SELECT id, name, folder_path, drive_link, media_type
		FROM clips
		WHERE LOWER(REPLACE(folder_path, ' ', '')) LIKE ?
		   OR LOWER(REPLACE(name, ' ', '')) LIKE ?
		LIMIT 100
	`, "%"+queryNorm+"%", "%"+queryNorm+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FolderResult
	for rows.Next() {
		var r FolderResult
		var id string
		var mediaType sql.NullString
		var name, folderPath string
		if err := rows.Scan(&id, &name, &folderPath, &r.Link, &mediaType); err == nil {
			r.Source = "clip_drive"
			r.DriveID = id
			r.Name = name
			r.Path = folderPath
			if mediaType.Valid {
				r.MediaType = mediaType.String
			}
			results = append(results, r)
		}
	}
	return results, nil
}

func normalizeQueryFromDB(q string) string {
	q = strings.ToLower(q)
	q = strings.ReplaceAll(q, " ", "")
	q = strings.ReplaceAll(q, "-", "")
	return q
}

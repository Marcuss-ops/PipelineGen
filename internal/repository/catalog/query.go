package catalog

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// CatalogRecord represents a generic record from any of the media databases.
type CatalogRecord struct {
	ID        string   `json:"id"`
	Source    string   `json:"source"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Link      string   `json:"link"`
	DriveID   string   `json:"drive_id,omitempty"`
	TopicSlug string   `json:"topic_slug,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Duration  int      `json:"duration,omitempty"`
	Group     string   `json:"group,omitempty"`
}

type Repository struct {
	dataDir string
}

func NewRepository(dataDir string) *Repository {
	return &Repository{dataDir: dataDir}
}

func (r *Repository) SearchAll(q string) ([]CatalogRecord, error) {
	var results []CatalogRecord

	// Search Stock DB
	if stockResults, err := r.SearchStock(q); err == nil {
		results = append(results, stockResults...)
	}

	// Search Artlist DB
	if artlistResults, err := r.SearchArtlist(q); err == nil {
		results = append(results, artlistResults...)
	}

	// Search Clips DB
	if clipsResults, err := r.SearchClips(q); err == nil {
		results = append(results, clipsResults...)
	}

	return results, nil
}

func (r *Repository) SearchStock(q string) ([]CatalogRecord, error) {
	dbPath := filepath.Join(r.dataDir, "stock.db.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	defer db.Close()

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

	// 2. Fallback to clips table
	if len(results) == 0 {
		clipRows, err := db.Query(`
			SELECT id, name, folder_path, drive_link, media_type, tags, source, duration
			FROM clips
			WHERE (source = 'stock' OR media_type = 'stock')
			  AND (LOWER(REPLACE(folder_path, ' ', '')) LIKE ?
			   OR LOWER(REPLACE(name, ' ', '')) LIKE ?)
			LIMIT 100
		`, "%"+q+"%", "%"+q+"%")
		if err == nil {
			defer clipRows.Close()
			for clipRows.Next() {
				var rec CatalogRecord
				var mediaType, tagsRaw sql.NullString
				if err := clipRows.Scan(&rec.ID, &rec.Name, &rec.Path, &rec.Link, &mediaType, &tagsRaw, &rec.Source, &rec.Duration); err == nil {
					rec.DriveID = rec.ID
					if mediaType.Valid {
						rec.MediaType = mediaType.String
					}
					if tagsRaw.Valid {
						rec.Tags = parseTags(tagsRaw.String)
					}
					results = append(results, rec)
				}
			}
		}
	}

	return results, nil
}

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

func (r *Repository) SearchClips(q string) ([]CatalogRecord, error) {
	dbPath := filepath.Join(r.dataDir, "clips.db.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	queryNorm := strings.ToLower(strings.ReplaceAll(q, " ", ""))

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
				rec.Tags = parseTags(tagsRaw.String)
			}
			results = append(results, rec)
		}
	}
	return results, nil
}

func parseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err == nil {
			return tags
		}
	}
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

package clip

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// ArtlistSource provides clips from the Artlist SQLite database
type ArtlistSource struct {
	dbPath string
	db     *sql.DB
}

// NewArtlistSource creates a new Artlist clip source
func NewArtlistSource(dbPath string) *ArtlistSource {
	return &ArtlistSource{dbPath: dbPath}
}

// Connect opens the database connection
func (a *ArtlistSource) Connect() error {
	db, err := sql.Open("sqlite3", a.dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("failed to open Artlist DB: %w", err)
	}
	a.db = db
	return nil
}

// Close closes the database connection
func (a *ArtlistSource) Close() {
	if a.db != nil {
		a.db.Close()
	}
}

// SearchClips searches Artlist clips by query terms
func (a *ArtlistSource) SearchClips(query string, maxResults int) ([]IndexedClip, error) {
	if a.db == nil {
		return nil, fmt.Errorf("Artlist DB not connected")
	}

	if maxResults == 0 {
		maxResults = 20
	}

	// Build search terms from query
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return []IndexedClip{}, nil
	}

	// Search in video title/description via the search_terms table
	// Join video_links with search_terms to find matching clips
	likePatterns := make([]string, len(terms))
	args := make([]interface{}, len(terms))
	for i, t := range terms {
		likePatterns[i] = "st.term LIKE ?"
		args[i] = "%" + t + "%"
	}

	whereClause := strings.Join(likePatterns, " OR ")

	queryStr := fmt.Sprintf(`
		SELECT DISTINCT vl.id, vl.video_id, vl.url, vl.source,
			vl.width, vl.height, vl.duration, vl.file_size,
			vl.downloaded, vl.download_path,
			st.term as search_term,
			c.name as category_name
		FROM video_links vl
		LEFT JOIN search_terms st ON vl.search_term_id = st.id
		LEFT JOIN categories c ON vl.category_id = c.id
		WHERE vl.source = 'artlist' AND (%s)
		ORDER BY vl.duration DESC
		LIMIT ?
	`, whereClause)

	args = append(args, maxResults)

	rows, err := a.db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query Artlist: %w", err)
	}
	defer rows.Close()

	var clips []IndexedClip

	for rows.Next() {
		var (
			id           int
			videoID      string
			url          string
			source       string
			width        int
			height       int
			duration     int
			fileSize     sql.NullFloat64
			downloaded   int
			downloadPath sql.NullString
			searchTerm   sql.NullString
			categoryName sql.NullString
		)

		err := rows.Scan(&id, &videoID, &url, &source,
			&width, &height, &duration, &fileSize,
			&downloaded, &downloadPath,
			&searchTerm, &categoryName)
		if err != nil {
			logger.Warn("Failed to scan Artlist row",
				zap.Int("row_id", id),
				zap.Error(err))
			continue
		}

		// Build clip name from search term
		name := "artlist clip"
		if searchTerm.Valid {
			name = searchTerm.String
		}

		// Build tags
		var tags []string
		if searchTerm.Valid {
			for _, t := range strings.Fields(searchTerm.String) {
				if len(t) > 2 {
					tags = append(tags, strings.ToLower(t))
				}
			}
		}
		if categoryName.Valid {
			tags = append(tags, strings.ToLower(categoryName.String))
		}
		tags = append(tags, "artlist")

		// Build drive-like link
		driveLink := url
		if downloadPath.Valid {
			driveLink = downloadPath.String
		}

		// Build folder path with valid category name
		folderPath := "Artlist/unknown"
		if categoryName.Valid && categoryName.String != "" {
			folderPath = fmt.Sprintf("Artlist/%s", categoryName.String)
		}

		resolution := "unknown"
		if width > 0 && height > 0 {
			resolution = fmt.Sprintf("%dx%d", width, height)
		}

		clip := IndexedClip{
			ID:           fmt.Sprintf("artlist_%s_%d", videoID, id),
			Name:         name,
			Filename:     fmt.Sprintf("artlist_%s.mp4", videoID),
			FolderID:     "artlist",
			FolderPath:   folderPath,
			Group:        "artlist",
			DriveLink:    driveLink,
			DownloadLink: url,
			Resolution:   resolution,
			Duration:     float64(duration),
			Width:        width,
			Height:       height,
			Size:         int64(fileSize.Float64 * 1024 * 1024), // Convert MB to bytes
			MimeType:     "video/mp4",
			Tags:         tags,
			ModifiedAt:   time.Now(),
			IndexedAt:    time.Now(),
		}

		clips = append(clips, clip)
	}

	return clips, nil
}

// GetAllCategories returns all Artlist categories
func (a *ArtlistSource) GetAllCategories() ([]map[string]interface{}, error) {
	if a.db == nil {
		return nil, fmt.Errorf("Artlist DB not connected")
	}

	rows, err := a.db.Query("SELECT id, name, description FROM categories")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []map[string]interface{}
	for rows.Next() {
		var id int
		var name, desc string
		rows.Scan(&id, &name, &desc)
		cats = append(cats, map[string]interface{}{
			"id":          id,
			"name":        name,
			"description": desc,
		})
	}

	return cats, nil
}

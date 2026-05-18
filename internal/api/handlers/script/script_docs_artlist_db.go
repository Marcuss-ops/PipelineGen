package script

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/pkg/sliceutil"
	"velox/go-master/internal/pkg/sqlutil"

	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

// ArtlistDBClient handles queries to the Artlist SQLite database.
type ArtlistDBClient struct {
	dbPath string
}

// NewArtlistDBClient creates a new ArtlistDBClient.
func NewArtlistDBClient(nodeScraperDir string) *ArtlistDBClient {
	return &ArtlistDBClient{
		dbPath: filepath.Join(nodeScraperDir, "artlist_videos.db"),
	}
}

// SearchClipsByKeywords searches for Artlist clips matching tokens from the provided keywords.
func (c *ArtlistDBClient) SearchClipsByKeywords(keywords []string, limit int) ([]association.ScoredMatch, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	log := zap.L()
	sqliteDB, err := storage.OpenSQLiteDB(c.dbPath, log)
	if err != nil {
		return nil, fmt.Errorf("failed to open artlist db: %v", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	// Tokenize all keywords for maximum reach
	var tokens []string
	for _, kw := range keywords {
		parts := strings.Fields(strings.ToLower(kw))
		for _, p := range parts {
			if len(p) > 2 {
				tokens = append(tokens, p)
			}
		}
	}
	tokens = sliceutil.UniqueStrings(tokens)

	if len(tokens) == 0 {
		return nil, nil
	}

	// Build query joining with search_terms for better matching
	queryBase := `
		SELECT v.url, v.video_id, v.width, v.height, v.duration 
		FROM video_links v
		LEFT JOIN search_terms s ON v.search_term_id = s.id
		WHERE `

	columns := []string{"s.term", "v.url"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(tokens, columns)
	if conditionSQL == "" {
		return nil, nil
	}

	query := queryBase + conditionSQL + " GROUP BY v.url LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		fmt.Printf("DB QUERY ERROR: %v\n", err)
		return nil, fmt.Errorf("query failed: %v", err)
	}
	defer rows.Close()

	var matches []association.ScoredMatch
	for rows.Next() {
		var url, videoID string
		var width, height, duration int
		if err := rows.Scan(&url, &videoID, &width, &height, &duration); err != nil {
			continue
		}

		matches = append(matches, association.ScoredMatch{
			Title:  videoID,
			Link:   url,
			Source: "artlist_db",
			Score:  100,
		})
	}

	return matches, nil
}

// LookupClipURLByVideoID returns the best known URL for a given Artlist video ID.
func (c *ArtlistDBClient) LookupClipURLByVideoID(videoID string) (string, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return "", nil
	}

	log := zap.L()
	sqliteDB, err := storage.OpenSQLiteDB(c.dbPath, log)
	if err != nil {
		return "", fmt.Errorf("failed to open artlist db: %v", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	var url string
	err = db.QueryRow(`
		SELECT url
		FROM video_links
		WHERE video_id = ?
		ORDER BY id ASC
		LIMIT 1
	`, videoID).Scan(&url)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("failed to lookup artlist url: %v", err)
	}

	return strings.TrimSpace(url), nil
}

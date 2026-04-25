package script

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

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
func (c *ArtlistDBClient) SearchClipsByKeywords(keywords []string, limit int) ([]scoredMatch, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	db, err := sql.Open("sqlite3", c.dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open artlist db: %v", err)
	}
	defer db.Close()

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
	tokens = uniqueStrings(tokens)

	if len(tokens) == 0 {
		return nil, nil
	}

	// Build query joining with search_terms for better matching
	queryBase := `
		SELECT v.url, v.video_id, v.width, v.height, v.duration 
		FROM video_links v
		LEFT JOIN search_terms s ON v.search_term_id = s.id
		WHERE `

	var conditions []string
	var args []interface{}
	for _, token := range tokens {
		conditions = append(conditions, "(s.term LIKE ? OR v.url LIKE ?)")
		pattern := "%" + token + "%"
		args = append(args, pattern, pattern)
	}

	query := queryBase + "(" + strings.Join(conditions, " OR ") + ") GROUP BY v.url LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		fmt.Printf("DB QUERY ERROR: %v\n", err)
		return nil, fmt.Errorf("query failed: %v", err)
	}
	defer rows.Close()

	var matches []scoredMatch
	for rows.Next() {
		var url, videoID string
		var width, height, duration int
		if err := rows.Scan(&url, &videoID, &width, &height, &duration); err != nil {
			continue
		}

		matches = append(matches, scoredMatch{
			Title:  videoID,
			Link:   url,
			Source: "artlist_db",
			Score:  100,
		})
	}

	return matches, nil
}

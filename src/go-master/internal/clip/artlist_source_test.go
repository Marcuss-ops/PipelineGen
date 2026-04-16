package clip

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Helper to create a temporary test database with sample data
func createTestArtlistDB(t *testing.T) string {
	t.Helper()

	// Create temp file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_artlist.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	// Create tables
	schema := `
	CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT
	);

	CREATE TABLE IF NOT EXISTS search_terms (
		id INTEGER PRIMARY KEY,
		term TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS video_links (
		id INTEGER PRIMARY KEY,
		video_id TEXT NOT NULL,
		url TEXT NOT NULL,
		source TEXT NOT NULL,
		width INTEGER,
		height INTEGER,
		duration INTEGER,
		file_size REAL,
		downloaded INTEGER DEFAULT 0,
		download_path TEXT,
		search_term_id INTEGER,
		category_id INTEGER,
		FOREIGN KEY (search_term_id) REFERENCES search_terms(id),
		FOREIGN KEY (category_id) REFERENCES categories(id)
	);
	`

	_, err = db.Exec(schema)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Insert test categories
	categories := []struct {
		id          int
		name        string
		description string
	}{
		{1, "Technology", "Tech and AI related videos"},
		{2, "Nature", "Nature and landscape videos"},
		{3, "Business", "Business and startup videos"},
	}

	for _, cat := range categories {
		_, err = db.Exec("INSERT INTO categories (id, name, description) VALUES (?, ?, ?)",
			cat.id, cat.name, cat.description)
		if err != nil {
			t.Fatalf("Failed to insert category: %v", err)
		}
	}

	// Insert test search terms
	searchTerms := []struct {
		id   int
		term string
	}{
		{1, "artificial intelligence robot technology"},
		{2, "nature landscape sunset mountains"},
		{3, "business meeting startup office"},
		{4, "computer programming coding software"},
		{5, "ocean waves beach summer"},
		{6, "technology innovation future digital"},
	}

	for _, term := range searchTerms {
		_, err = db.Exec("INSERT INTO search_terms (id, term) VALUES (?, ?)",
			term.id, term.term)
		if err != nil {
			t.Fatalf("Failed to insert search term: %v", err)
		}
	}

	// Insert test video links
	videos := []struct {
		id           int
		videoID      string
		url          string
		source       string
		width        int
		height       int
		duration     int
		fileSize     float64
		searchTermID int
		categoryID   int
	}{
		{1, "vid001", "https://artlist.io/video/001", "artlist", 1920, 1080, 30, 50.5, 1, 1},
		{2, "vid002", "https://artlist.io/video/002", "artlist", 3840, 2160, 45, 120.0, 2, 2},
		{3, "vid003", "https://artlist.io/video/003", "artlist", 1920, 1080, 25, 40.0, 3, 3},
		{4, "vid004", "https://artlist.io/video/004", "artlist", 1920, 1080, 60, 80.0, 4, 1},
		{5, "vid005", "https://artlist.io/video/005", "artlist", 1920, 1080, 35, 55.0, 5, 2},
		{6, "vid006", "https://artlist.io/video/006", "artlist", 3840, 2160, 40, 95.0, 6, 1},
		{7, "vid007", "https://artlist.io/video/007", "artlist", 1920, 1080, 20, 30.0, 1, 1},
		{8, "vid008", "https://artlist.io/video/008", "artlist", 1920, 1080, 50, 70.0, 2, 2},
	}

	for _, vid := range videos {
		_, err = db.Exec(`INSERT INTO video_links 
			(id, video_id, url, source, width, height, duration, file_size, search_term_id, category_id) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			vid.id, vid.videoID, vid.url, vid.source,
			vid.width, vid.height, vid.duration, vid.fileSize,
			vid.searchTermID, vid.categoryID)
		if err != nil {
			t.Fatalf("Failed to insert video: %v", err)
		}
	}

	return dbPath
}

// TestArtlistSource_Connect tests database connection
func TestArtlistSource_Connect(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	err := source.Connect()
	if err != nil {
		t.Fatalf("Failed to connect to test DB: %v", err)
	}
	defer source.Close()

	t.Log("✅ Successfully connected to test Artlist DB")
}

// TestArtlistSource_SearchByKeywords tests searching clips by keywords
func TestArtlistSource_SearchByKeywords(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	tests := []struct {
		name       string
		query      string
		maxResults int
		expectMin  int
	}{
		{
			name:       "Technology search",
			query:      "artificial intelligence robot",
			maxResults: 10,
			expectMin:  1,
		},
		{
			name:       "Nature search",
			query:      "nature landscape sunset",
			maxResults: 10,
			expectMin:  1,
		},
		{
			name:       "Business search",
			query:      "business meeting office",
			maxResults: 10,
			expectMin:  1,
		},
		{
			name:       "No results search",
			query:      "xyzabc unknown topic",
			maxResults: 10,
			expectMin:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clips, err := source.SearchClips(tt.query, tt.maxResults)
			if err != nil {
				t.Fatalf("SearchClips failed: %v", err)
			}

			if len(clips) < tt.expectMin {
				t.Errorf("Expected at least %d clips, got %d", tt.expectMin, len(clips))
			}

			t.Logf("✅ Search '%s': %d results", tt.query, len(clips))
			for i, clip := range clips {
				t.Logf("   [%d] %s (duration: %.0fs, tags: %v)",
					i+1, clip.Name, clip.Duration, clip.Tags[:min(3, len(clip.Tags))])
			}
		})
	}
}

// TestArtlistSource_SearchMultipleKeywords tests searching with multiple keywords
func TestArtlistSource_SearchMultipleKeywords(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	// Search with multiple terms
	clips, err := source.SearchClips("technology innovation future digital", 10)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	if len(clips) == 0 {
		t.Error("Expected clips for multi-keyword search")
	}

	t.Logf("✅ Multi-keyword search: %d results", len(clips))
}

// TestArtlistSource_EmptyDatabase tests behavior with empty database
func TestArtlistSource_EmptyDatabase(t *testing.T) {
	// Create empty DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "empty.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to create empty DB: %v", err)
	}

	// Create empty tables only - match production schema
	schema := `
	CREATE TABLE IF NOT EXISTS categories (id INTEGER PRIMARY KEY, name TEXT, description TEXT);
	CREATE TABLE IF NOT EXISTS search_terms (id INTEGER PRIMARY KEY, term TEXT);
	CREATE TABLE IF NOT EXISTS video_links (
		id INTEGER PRIMARY KEY,
		video_id TEXT,
		url TEXT,
		source TEXT,
		width INTEGER,
		height INTEGER,
		duration INTEGER,
		file_size REAL,
		downloaded INTEGER,
		download_path TEXT,
		search_term_id INTEGER,
		category_id INTEGER
	);
	`
	_, err = db.Exec(schema)
	db.Close()

	if err != nil {
		t.Fatalf("Failed to create empty schema: %v", err)
	}

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	clips, err := source.SearchClips("test", 10)
	if err != nil {
		t.Fatalf("SearchClips failed on empty DB: %v", err)
	}

	if len(clips) != 0 {
		t.Errorf("Expected 0 clips from empty DB, got %d", len(clips))
	}

	t.Log("✅ Empty database handled correctly")
}

// TestArtlistSource_NoConnection tests behavior when not connected
func TestArtlistSource_NoConnection(t *testing.T) {
	source := NewArtlistSource("/tmp/nonexistent.db")
	// Don't call Connect()

	_, err := source.SearchClips("test", 10)
	if err == nil {
		t.Error("Expected error when not connected")
	}

	t.Log("✅ No connection error handled correctly")
}

// TestArtlistSource_MaxResults tests that maxResults limits results
func TestArtlistSource_MaxResults(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	// Search with maxResults=2
	clips, err := source.SearchClips("technology", 2)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	if len(clips) > 2 {
		t.Errorf("Expected max 2 results, got %d", len(clips))
	}

	t.Logf("✅ MaxResults limit: requested=2, got=%d", len(clips))
}

// TestArtlistSource_ClipMetadata tests that clip metadata is correctly populated
func TestArtlistSource_ClipMetadata(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	clips, err := source.SearchClips("robot", 5)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	if len(clips) == 0 {
		t.Skip("No clips found to test metadata")
	}

	clip := clips[0]

	// Verify required fields
	if clip.ID == "" {
		t.Error("Expected clip ID to be set")
	}

	if !strings.HasPrefix(clip.ID, "artlist_") {
		t.Errorf("Expected ID to start with 'artlist_', got: %s", clip.ID)
	}

	if clip.MimeType != "video/mp4" {
		t.Errorf("Expected mime type 'video/mp4', got: %s", clip.MimeType)
	}

	if clip.Duration == 0 {
		t.Error("Expected duration to be set")
	}

	if len(clips[0].Tags) == 0 {
		t.Error("Expected tags to be populated")
	}

	// Check that 'artlist' tag is present
	hasArtlistTag := false
	for _, tag := range clip.Tags {
		if tag == "artlist" {
			hasArtlistTag = true
			break
		}
	}

	if !hasArtlistTag {
		t.Error("Expected 'artlist' tag in clip tags")
	}

	t.Logf("✅ Clip metadata verified:")
	t.Logf("   ID: %s", clip.ID)
	t.Logf("   Name: %s", clip.Name)
	t.Logf("   Duration: %.0f", clip.Duration)
	t.Logf("   Resolution: %s", clip.Resolution)
	t.Logf("   Tags: %v", clip.Tags)
	t.Logf("   FolderPath: %s", clip.FolderPath)
}

// TestArtlistSource_CategoryFilter tests searching by category
func TestArtlistSource_CategoryFilter(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	// Search for technology clips
	techClips, err := source.SearchClips("technology computer", 10)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	// Search for nature clips
	natureClips, err := source.SearchClips("nature landscape", 10)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	t.Logf("✅ Category search:")
	t.Logf("   Technology clips: %d", len(techClips))
	t.Logf("   Nature clips: %d", len(natureClips))

	// Verify categories in folder path
	for _, clip := range techClips {
		if !strings.Contains(clip.FolderPath, "Technology") {
			t.Logf("⚠️  Technology clip not in Technology folder: %s", clip.FolderPath)
		}
	}
}

// TestArtlistSource_GetAllCategories tests getting all categories
func TestArtlistSource_GetAllCategories(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	categories, err := source.GetAllCategories()
	if err != nil {
		t.Fatalf("GetAllCategories failed: %v", err)
	}

	if len(categories) != 3 {
		t.Errorf("Expected 3 categories, got %d", len(categories))
	}

	t.Logf("✅ Got %d categories:", len(categories))
	for i, cat := range categories {
		t.Logf("   [%d] %s: %s", i+1, cat["name"], cat["description"])
	}
}

// TestArtlistSource_SearchTermMatching tests that search terms match correctly
func TestArtlistSource_SearchTermMatching(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	// Search for a specific term
	clips, err := source.SearchClips("ocean waves beach", 10)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	if len(clips) == 0 {
		t.Error("Expected to find clips for 'ocean waves beach' search")
	}

	// Verify that returned clips have matching search terms
	for _, clip := range clips {
		found := false
		for _, tag := range clip.Tags {
			if strings.Contains(tag, "ocean") || strings.Contains(tag, "waves") ||
				strings.Contains(tag, "beach") {
				found = true
				break
			}
		}

		if !found {
			t.Logf("⚠️  Clip '%s' doesn't have matching search terms in tags", clip.Name)
		}
	}

	t.Logf("✅ Search term matching: %d clips found", len(clips))
}

// TestArtlistSource_DuplicateResults tests handling of duplicate results
func TestArtlistSource_DuplicateResults(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	// Search with common term that might match multiple videos
	clips, err := source.SearchClips("technology", 20)
	if err != nil {
		t.Fatalf("SearchClips failed: %v", err)
	}

	// Check for duplicates
	seen := make(map[string]bool)
	duplicates := 0
	for _, clip := range clips {
		if seen[clip.ID] {
			duplicates++
		}
		seen[clip.ID] = true
	}

	if duplicates > 0 {
		t.Errorf("Found %d duplicate clips", duplicates)
	}

	t.Logf("✅ No duplicates: %d unique clips", len(clips))
}

// TestArtlistSource_IntegrationWithIndexer tests Artlist integration with indexer
func TestArtlistSource_IntegrationWithIndexer(t *testing.T) {
	dbPath := createTestArtlistDB(t)

	source := NewArtlistSource(dbPath)
	if err := source.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer source.Close()

	// Create indexer with Artlist source
	indexer := &Indexer{
		index: &ClipIndex{
			Version: "test",
			Clips:   []IndexedClip{}, // Empty Drive clips
		},
		cache:      NewSuggestionCache(100, 60000000000),
		artlistSrc: source,
	}

	suggester := NewSemanticSuggester(indexer)

	// Search for clips via suggester
	ctx := context.Background()
	results := suggester.SuggestForSentence(ctx, "artificial intelligence robot technology", 10, 0, "")

	if len(results) == 0 {
		t.Log("⚠️  No suggestions returned (Artlist clips may have low scores)")
		return
	}

	t.Logf("✅ Integration test: %d suggestions", len(results))
	for i, result := range results {
		t.Logf("   [%d] Score: %.2f, Type: %s, Name: %s",
			i+1, result.Score, result.MatchType, result.Clip.Name)
	}
}

// BenchmarkArtlistSource_Search benchmarks search performance
func BenchmarkArtlistSource_Search(b *testing.B) {
	// Create a larger test DB
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "benchmark.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		b.Fatalf("Failed to create DB: %v", err)
	}

	// Create schema
	_, err = db.Exec(`
		CREATE TABLE categories (id INTEGER PRIMARY KEY, name TEXT, description TEXT);
		CREATE TABLE search_terms (id INTEGER PRIMARY KEY, term TEXT);
		CREATE TABLE video_links (
			id INTEGER PRIMARY KEY, video_id TEXT, url TEXT, source TEXT,
			width INTEGER, height INTEGER, duration INTEGER, file_size REAL,
			search_term_id INTEGER, category_id INTEGER
		);
	`)
	db.Close()

	// Insert 100 test videos
	source := NewArtlistSource(dbPath)
	source.Connect()
	defer source.Close()

	// Manually insert more data
	source.db.Exec("INSERT INTO search_terms (id, term) VALUES (1, 'technology computer AI')")
	for i := 1; i <= 100; i++ {
		source.db.Exec(
			"INSERT INTO video_links (video_id, url, source, width, height, duration, file_size, search_term_id, category_id) VALUES (?, ?, ?, 1920, 1080, 30, 50.0, 1, 1)",
			fmt.Sprintf("vid%03d", i), fmt.Sprintf("https://artlist.io/video/%03d", i), "artlist")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = source.SearchClips("technology", 10)
	}
}

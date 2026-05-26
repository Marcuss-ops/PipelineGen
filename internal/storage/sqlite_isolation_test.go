package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// expectedTables defines the TARGET tables for the single unified database.
// All databases have been consolidated into data/velox/velox.db.sqlite.
var expectedTables = map[string][]string{
	"velox/velox.db.sqlite": {
		// System tables
		"scripts",
		"monitored_sources",
		"harvester_jobs",
		"script_stock_matches",
		"video_stats_history",
		"artlist_runs",
		"script_sections",
		"pipeline_runs",
		"pipeline_run_items",
		"schema_migrations",
		"jobs",
		"job_events",
		"asset_index",
		"asset_links",
		"asset_tree_nodes",
		"api_requests",
		// Media content tables (merged from media.db)
		"media_assets",
		"clip_folders",
		"segment_embeddings",
		"sketchfab_models",
		"subjects",
		"voiceovers",
	},
}

// TestDBIsolation verifies the unified database has only expected tables.
func TestDBIsolation(t *testing.T) {
	dataDir := filepath.Join("..", "..", "data")

	for dbPathSuffix, expected := range expectedTables {
		t.Run(dbPathSuffix, func(t *testing.T) {
			dbPath := filepath.Join(dataDir, dbPathSuffix)
			db, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				t.Skipf("Database %s not found: %v", dbPathSuffix, err)
				return
			}
			defer db.Close()

			// Get actual tables (excluding sqlite_* and schema_migrations for now)
			rows, err := db.Query(`
				SELECT name FROM sqlite_master 
				WHERE type IN ('table', 'view') 
				AND name NOT LIKE 'sqlite_%'
				ORDER BY name
			`)
			if err != nil {
				t.Fatalf("Failed to query tables: %v", err)
			}
			defer rows.Close()

			actualSet := make(map[string]bool)
			var actualTables []string
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					t.Fatalf("Failed to scan table name: %v", err)
				}
				actualSet[name] = true
				actualTables = append(actualTables, name)
			}

			// Check for unexpected tables
			expectedSet := make(map[string]bool)
			for _, table := range expected {
				expectedSet[table] = true
			}

			for _, table := range actualTables {
				if !expectedSet[table] {
					t.Errorf("Database %s has unexpected table: %s", dbPathSuffix, table)
				}
			}

			// Check for missing expected tables (excluding schema_migrations which is auto-created)
			for _, table := range expected {
				if table == "schema_migrations" {
					continue
				}
				if !actualSet[table] {
					t.Errorf("Database %s missing expected table: %s", dbPathSuffix, table)
				}
			}

			// Log actual tables for debugging
			t.Logf("Database %s tables: %v", dbPathSuffix, actualTables)
		})
	}
}

// TestFTS5Fallback verifies that LIKE search works on the unified database.
func TestFTS5Fallback(t *testing.T) {
	dataDir := filepath.Join("..", "..", "data")
	dbPath := filepath.Join(dataDir, "velox/velox.db.sqlite")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skipf("Database %s not found, skipping", dbPath)
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Check for FTS5 by trying to create a virtual table
	var hasFTS5 bool
	_, err = db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS _test_fts USING fts5(content=media_assets, content_rowid='rowid')")
	if err == nil {
		hasFTS5 = true
		db.Exec("DROP TABLE IF EXISTS _test_fts")
	}

	// Test LIKE search on media_assets (should work with or without FTS5)
	rows, err := db.Query(`
		SELECT id, name FROM media_assets 
		WHERE id = ? OR name LIKE ? 
		LIMIT 10
	`, "test_iso", "%test%")
	if err != nil {
		t.Fatalf("LIKE search failed: %v", err)
	}
	defer rows.Close()

	t.Log("LIKE search works on media_assets (fallback path confirmed)")

	if hasFTS5 {
		t.Log("FTS5 is available - full-text search enabled (via virtual table)")
	} else {
		t.Log("FTS5 not available - using LIKE fallback (expected in current driver)")
	}
}

// TestSegmentEmbeddingsLocation verifies segment_embeddings is in the unified DB.
func TestSegmentEmbeddingsLocation(t *testing.T) {
	dataDir := filepath.Join("..", "..", "data")

	// Check the unified database
	dbName := "velox/velox.db.sqlite"
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, dbName))
	if err != nil {
		t.Skipf("Database %s not found", dbName)
		return
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type='table' AND name='segment_embeddings'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to check %s: %v", dbName, err)
	}

	if count == 0 {
		t.Errorf("Database %s should have segment_embeddings table", dbName)
	} else {
		t.Logf("✓ %s has segment_embeddings (expected)", dbName)
	}
}
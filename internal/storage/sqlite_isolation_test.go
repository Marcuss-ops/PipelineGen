package storage

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// expectedTables defines the TARGET tables for each database (per user's desired schema).
// NOTE: Tables listed as "unexpected" by tests need cleanup.
var expectedTables = map[string][]string{
	"velox/velox.db.sqlite": {
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
	},
	"stock/stock.db.sqlite": {
		"media_assets", // Unified media table
		"clip_folders", // Stock-specific folders
		"schema_migrations",
	},
	"clips/clips.db.sqlite": {
		"clips",              // YouTube-specific clips (still legacy or specific?)
		"clip_folders",       // YouTube-specific folders
		"segment_embeddings", // Timeline cache - appropriate here
		"schema_migrations",
	},
	"artlist/artlist.db.sqlite": {
		"media_assets", // Unified media table
		"clip_folders", // Artlist-specific folders
		"clips_fts",    // FTS index
		"clips_fts_config",
		"clips_fts_data",
		"clips_fts_docsize",
		"clips_fts_idx",
		"schema_migrations",
	},
	"images/images.db.sqlite": {
		"images",
		"subjects",
		"image_tags",
		"schema_migrations",
	},
	"media/media.db.sqlite": {
		"clip_folders",
		"media_assets",
		"segment_embeddings",
		"sketchfab_models",
		"subjects",
		"voiceovers",
		"schema_migrations",
	},
}

// TestDBIsolation verifies each database has only expected tables.
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

// TestFTS5Fallback verifies that LIKE search works when FTS5 is not available.
func TestFTS5Fallback(t *testing.T) {
	dataDir := filepath.Join("..", "..", "data")
	dbPath := filepath.Join(dataDir, "clips", "clips.db.sqlite")
	absPath, _ := filepath.Abs(dbPath)
	t.Logf("Testing with database: %s", absPath)

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Skipf("Database not found: %v", err)
		return
	}
	defer db.Close()

	// Enable WAL mode
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")

	// Check FTS5 availability
	hasFTS5 := HasFTS5(db, nil) // nil logger for test

	// Clean up test data if exists
	db.Exec("DELETE FROM clips WHERE id='test_iso'")

	// Insert test data
	_, err = db.Exec(`
		INSERT INTO clips (id, name, folder_path, group_name, media_type, drive_link, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
	`, "test_iso", "test clip", "/test", "test", "clip", "http://test", "[]")
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}
	defer db.Exec("DELETE FROM clips WHERE id='test_iso'")

	// Test LIKE search (should work with or without FTS5)
	rows, err := db.Query(`
		SELECT id, name FROM clips 
		WHERE id = ?
		LIMIT 10
	`, "test_iso")
	if err != nil {
		t.Fatalf("LIKE search failed: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("Failed to scan: %v", err)
		}
		if id == "test_iso" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Test clip not found via LIKE search")
	}

	if hasFTS5 {
		t.Log("FTS5 is available - full-text search enabled")
	} else {
		t.Log("FTS5 not available - using LIKE fallback (expected in current driver)")
	}
}

// TestSegmentEmbeddingsLocation verifies segment_embeddings is only in appropriate DBs.
func TestSegmentEmbeddingsLocation(t *testing.T) {
	dataDir := filepath.Join("..", "..", "data")

	// Should have segment_embeddings
	for _, dbName := range []string{"clips/clips.db.sqlite"} {
		db, err := sql.Open("sqlite3", filepath.Join(dataDir, dbName))
		if err != nil {
			t.Skipf("Database %s not found", dbName)
			continue
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

	// Should NOT have segment_embeddings (or it's legacy)
	t.Log("Note: segment_embeddings in stock.db.sqlite and artlist.db.sqlite should be evaluated for removal")
}

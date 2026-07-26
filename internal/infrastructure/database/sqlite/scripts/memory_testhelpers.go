package scripts

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newMemoryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := `
		CREATE TABLE gemma_script_outputs (
			id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			mode TEXT NOT NULL,
			language TEXT NOT NULL DEFAULT 'en',
			title TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL DEFAULT '',
			normalized_input TEXT NOT NULL DEFAULT '',
			input_hash TEXT NOT NULL,
			output_text TEXT NOT NULL DEFAULT '',
			output_json TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			word_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(channel_id, mode, input_hash)
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

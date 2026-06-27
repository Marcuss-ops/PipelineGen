// Package idempotency — test helpers for the idempotency middleware.
//
// PG-005 / Wave 16 (June 2026): OpenTestDB centralises the in-memory
// SQLite boilerplate previously duplicated in idempotency_test.go.
// Test files that need an idempotency store backed by an ephemeral
// SQLite database import only this package (and the application port) —
// they never import database/sql or the sqlite3 driver directly,
// keeping the api/ layer clean per the Wave 16 database/sql ratchet.
package idempotency

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"

	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
)

const idempotencySchemaDDL = `
	CREATE TABLE IF NOT EXISTS idempotency_keys (
		key TEXT PRIMARY KEY,
		body_hash TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'in_flight',
		response_status INTEGER NOT NULL DEFAULT 0,
		response_body TEXT NOT NULL DEFAULT '',
		response_content_type TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		last_replayed_at TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at
		ON idempotency_keys(expires_at);
`

// OpenTestDB creates an in-memory SQLite-backed IdempotencyStore
// suitable for middleware tests. The returned store is backed by a
// shared-cache in-memory database; schema is applied automatically.
// Callers treat the return value as an opaque mw.IdempotencyStore —
// they never import database/sql or the sqlite3 driver directly.
//
// The cleanup function closes the underlying database handle and
// removes the temporary directory. Callers must invoke it (typically
// via t.Cleanup) to avoid leaking file descriptors and disk space.
func OpenTestDB() (mw.IdempotencyStore, func(), error) {
	tmpDir, err := os.MkdirTemp("", "idem-test-*")
	if err != nil {
		return nil, nil, fmt.Errorf("idempotency.OpenTestDB: mkdir temp: %w", err)
	}
	// shared-cache mode keeps the database alive across connections
	// within the test process — essential for multi-goroutine tests
	// (TestIdempotency_ConcurrentSameKeyYieldsOneCacheFillOneConflict).
	dsn := filepath.Join(tmpDir, "idem.sqlite") + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, nil, fmt.Errorf("idempotency.OpenTestDB: open: %w", err)
	}

	if _, err := db.Exec(idempotencySchemaDDL); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		return nil, nil, fmt.Errorf("idempotency.OpenTestDB: schema: %w", err)
	}

	store := NewSQLiteRepository(db)
	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}
	return store, cleanup, nil
}

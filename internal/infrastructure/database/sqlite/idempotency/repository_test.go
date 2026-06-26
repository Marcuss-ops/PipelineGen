// Package idempotency — integration-style tests for
// SQLiteRepository. Tests run against an in-memory SQLite database
// using the canonical schema from migration 095. No mocking of the
// storage layer required.
package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
)

// openTestDB creates a one-shot in-memory SQLite db with the
// idempotency_keys table installed. Each call returns a fresh
// database so tests don't share state.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// file::memory:?cache=shared would otherwise leak across tests; use a
	// per-test unique DSN via a temp file and :memory: explicitly.
	tmp := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sql.Open("sqlite3", tmp+"?_journal_mode=WAL&mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const ddl = `
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
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	return db
}

func TestTryInsert_InsertSucceeds(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	rec, dup, err := repo.TryInsert(ctx, "k1", "h1")
	if err != nil {
		t.Fatalf("TryInsert: %v", err)
	}
	if dup {
		t.Fatalf("expected fresh insert, got duplicate=true")
	}
	if rec.Key != "k1" || rec.BodyHash != "h1" {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if rec.Status != "in_flight" {
		t.Fatalf("expected in_flight, got %q", rec.Status)
	}
	if rec.ResponseStatus != 0 {
		t.Fatalf("expected response_status=0, got %d", rec.ResponseStatus)
	}
}

func TestTryInsert_DuplicateReportsAlreadyExists(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	if _, dup, err := repo.TryInsert(ctx, "k1", "h1"); err != nil || dup {
		t.Fatalf("first insert: dup=%v err=%v", dup, err)
	}
	_, dup, err := repo.TryInsert(ctx, "k1", "h2")
	if err != nil {
		t.Fatalf("second TryInsert: %v", err)
	}
	if !dup {
		t.Fatalf("expected duplicate=true on collision")
	}
}

func TestTryInsert_EmptyKeyRejected(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	_, _, err := repo.TryInsert(ctx, "", "h")
	if err == nil {
		t.Fatalf("expected error for empty key")
	}
}

func TestTryInsert_KeyLengthCap(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	longKey := strings.Repeat("a", 256)
	_, _, err := repo.TryInsert(ctx, longKey, "h")
	if err == nil {
		t.Fatalf("expected error for key > 255 chars")
	}
}

func TestComplete_TransitionsToCompleted(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	if _, _, err := repo.TryInsert(ctx, "k1", "h1"); err != nil {
		t.Fatalf("TryInsert: %v", err)
	}
	if err := repo.Complete(ctx, "k1", 200, []byte(`{"ok":true}`), "application/json"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	rec, err := repo.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Status != "completed" {
		t.Fatalf("expected completed, got %q", rec.Status)
	}
	if rec.ResponseStatus != 200 {
		t.Fatalf("response_status: got %d want 200", rec.ResponseStatus)
	}
	if string(rec.ResponseBody) != `{"ok":true}` {
		t.Fatalf("response_body mismatch: %q", string(rec.ResponseBody))
	}
	if rec.ResponseCT != "application/json" {
		t.Fatalf("response_content_type: got %q want application/json", rec.ResponseCT)
	}
}

func TestComplete_OnUnknownKeyReturnsNotFound(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	err := repo.Complete(ctx, "missing", 200, nil, "")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, mw.ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected ErrIdempotencyKeyNotFound, got %v", err)
	}
}

func TestGet_NotFoundReturnsSentinelError(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	_, err := repo.Get(ctx, "missing")
	if !errors.Is(err, mw.ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected ErrIdempotencyKeyNotFound, got %v", err)
	}
}

func TestDeleteExpired_RemovesOldRows(t *testing.T) {
	db := openTestDB(t)
	repo := NewSQLiteRepository(db)
	ctx := context.Background()

	// Insert two rows.
	if _, _, err := repo.TryInsert(ctx, "k1", "h"); err != nil {
		t.Fatalf("TryInsert k1: %v", err)
	}
	if _, _, err := repo.TryInsert(ctx, "k2", "h"); err != nil {
		t.Fatalf("TryInsert k2: %v", err)
	}
	// Fast-forward k1 by manually rolling its expires_at into the past.
	if _, err := db.ExecContext(ctx,
		`UPDATE idempotency_keys SET expires_at = '2000-01-01T00:00:00Z' WHERE key = 'k1'`); err != nil {
		t.Fatalf("expire k1: %v", err)
	}
	n, err := repo.DeleteExpired(ctx, nowTest())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d rows, want 1", n)
	}
	// k1 should be gone, k2 should still be there.
	if _, err := repo.Get(ctx, "k1"); !errors.Is(err, mw.ErrIdempotencyKeyNotFound) {
		t.Fatalf("expected k1 gone, got %v", err)
	}
	if _, err := repo.Get(ctx, "k2"); err != nil {
		t.Fatalf("expected k2 alive, got %v", err)
	}
}

func nowTest() time.Time {
	// Default to time.Now() — DeleteExpired sees k1 (rolled to
	// '2000-01-01T00:00:00Z') as expired, k2 (default +24h) as alive.
	return time.Now()
}

// Package assets — clips_statistics_test.go (Fase 3, July 2026)
//
// Unit tests for the canonical CountPersistedSince method added in
// Commit 2 of Fase 3. The helper is the real-DB cross-check for the
// /api/artlist/runs/:id state machine (Fase 3 Commit 1):
//
//	state machine Rule 2: Processed > 0 AND RealPersisted == 0
//	                    → PARTIAL_SUCCESS (zero-asset run cannot
//	                      be SUCCEEDED per the user spec literal)
//
// The CountPersistedSince helper powers the RealPersisted field of
// RunStatusCounts. Pure SQL helper; no composition-root concerns.
package assets

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestDBForCountPersistedSince opens a tempdir SQLite DB with
// the canonical media_assets schema (the minimal subset the
// CountPersistedSince query needs: id + source + created_at).
// Returns (*sql.DB, cleanup-fn). Caller defers the cleanup.
func newTestDBForCountPersistedSince(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "count_persisted_since_test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	require.NoError(t, err, "sql.Open for test DB")

	const schema = `
		CREATE TABLE IF NOT EXISTS media_assets (
			id          TEXT PRIMARY KEY,
			source      TEXT NOT NULL,
			created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT '',
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
	`
	_, err = db.Exec(schema)
	require.NoError(t, err, "create media_assets table for test")

	cleanup := func() { _ = db.Close() }
	return db, cleanup
}

// insertMediaAssetWithCreatedAt inserts one media_assets row with
// an explicit created_at value (the column's CURRENT_TIMESTAMP
// default is bypassed so the test can pin the time window).
func insertMediaAssetWithCreatedAt(t *testing.T, db *sql.DB, id, source, createdAt string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO media_assets (id, source, created_at) VALUES (?, ?, ?)`, id, source, createdAt)
	require.NoError(t, err, "insert media_assets row id=%s source=%s", id, source)
}

// TestCountPersistedSince_ValidTimeWindow is the canonical happy path:
// 3 artlist rows inserted at t=10:00:00 + 2 at t=11:00:00; query with
// since=t=10:30:00 returns 2 (the second batch).
func TestCountPersistedSince_ValidTimeWindow(t *testing.T) {
	db, cleanup := newTestDBForCountPersistedSince(t)
	defer cleanup()
	repo := NewClipsRepository(db, zap.NewNop())
	ctx := context.Background()

	// 3 rows at 10:00 UTC.
	insertMediaAssetWithCreatedAt(t, db, "a1", "artlist", "2026-07-12T10:00:00Z")
	insertMediaAssetWithCreatedAt(t, db, "a2", "artlist", "2026-07-12T10:00:00Z")
	insertMediaAssetWithCreatedAt(t, db, "a3", "artlist", "2026-07-12T10:00:00Z")
	// 2 rows at 11:00 UTC.
	insertMediaAssetWithCreatedAt(t, db, "a4", "artlist", "2026-07-12T11:00:00Z")
	insertMediaAssetWithCreatedAt(t, db, "a5", "artlist", "2026-07-12T11:00:00Z")
	// 1 row from a different source at 11:30 UTC (must NOT count).
	insertMediaAssetWithCreatedAt(t, db, "y1", "youtube", "2026-07-12T11:30:00Z")

	// Query: artlist rows at or after 10:30 UTC → expect 2.
	since, _ := time.Parse(time.RFC3339, "2026-07-12T10:30:00Z")
	count, err := repo.CountPersistedSince(ctx, "artlist", since)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "2 artlist rows created at 11:00+ UTC; the 10:00 batch is before the window, the youtube row is filtered by source")

	// Query: artlist rows at or after 09:00 UTC → expect 5 (all artlist).
	sinceEarly, _ := time.Parse(time.RFC3339, "2026-07-12T09:00:00Z")
	countEarly, err := repo.CountPersistedSince(ctx, "artlist", sinceEarly)
	require.NoError(t, err)
	assert.Equal(t, 5, countEarly, "5 artlist rows total; the youtube row is filtered by source")

	// Query: artlist rows at or after 12:00 UTC → expect 0.
	sinceLate, _ := time.Parse(time.RFC3339, "2026-07-12T12:00:00Z")
	countLate, err := repo.CountPersistedSince(ctx, "artlist", sinceLate)
	require.NoError(t, err)
	assert.Equal(t, 0, countLate, "no artlist rows at or after 12:00 UTC")
}

// TestCountPersistedSince_EmptySource pins the ErrEmptySource guard:
// passing source="" returns the typed sentinel (mirrors CountBySource
// discipline) so the caller can branch on errors.Is(..., ErrEmptySource).
func TestCountPersistedSince_EmptySource(t *testing.T) {
	db, cleanup := newTestDBForCountPersistedSince(t)
	defer cleanup()
	repo := NewClipsRepository(db, zap.NewNop())
	ctx := context.Background()

	since, _ := time.Parse(time.RFC3339, "2026-07-12T10:00:00Z")
	count, err := repo.CountPersistedSince(ctx, "", since)
	assert.ErrorIs(t, err, ErrEmptySource,
		"empty source must return the canonical ErrEmptySource sentinel (mirrors CountBySource godlike/07 discipline)")
	assert.Equal(t, 0, count, "count must be 0 on the error path")
}

// TestCountPersistedSince_ZeroTime pins the zero-ts guard:
// passing a zero time.Time would over-count every row, which is
// the same "fake-availability" anti-pattern as ErrEmptySource. The
// helper rejects zero ts with a typed error so the caller surfaces
// a config bug rather than a misleading over-count.
func TestCountPersistedSince_ZeroTime(t *testing.T) {
	db, cleanup := newTestDBForCountPersistedSince(t)
	defer cleanup()
	repo := NewClipsRepository(db, zap.NewNop())
	ctx := context.Background()

	insertMediaAssetWithCreatedAt(t, db, "a1", "artlist", "2026-07-12T10:00:00Z")

	_, err := repo.CountPersistedSince(ctx, "artlist", time.Time{})
	assert.Error(t, err, "zero time must return a typed error (would over-count every row)")
	assert.Contains(t, err.Error(), "ts is zero",
		"error must surface the canonical reason so operators can grep for the failure mode")
	assert.False(t, strings.Contains(err.Error(), "godlike/07 is not a valid string"),
		"sanity: error must be a real error, not a panic-induced string")
}

// TestCountPersistedSince_NilReceiver pins the nil-receiver guard:
// the helper must NOT panic when called on a nil *ClipsRepository.
// Mirrors the CountBySource godlike/07 fail-closed discipline.
func TestCountPersistedSince_NilReceiver(t *testing.T) {
	var repo *ClipsRepository = nil
	since, _ := time.Parse(time.RFC3339, "2026-07-12T10:00:00Z")
	count, err := repo.CountPersistedSince(context.Background(), "artlist", since)
	assert.Error(t, err, "nil receiver must return a typed error, not panic")
	assert.Contains(t, err.Error(), "nil repository")
	assert.Equal(t, 0, count)
}

// TestCountPersistedSince_NoRows pins the empty-table case: a fresh
// install with zero media_assets rows returns 0 with no error. This
// is the canonical "fresh install" + "no work yet" path for the
// state machine.
func TestCountPersistedSince_NoRows(t *testing.T) {
	db, cleanup := newTestDBForCountPersistedSince(t)
	defer cleanup()
	repo := NewClipsRepository(db, zap.NewNop())
	ctx := context.Background()

	since, _ := time.Parse(time.RFC3339, "2026-07-12T10:00:00Z")
	count, err := repo.CountPersistedSince(ctx, "artlist", since)
	require.NoError(t, err, "fresh install with empty media_assets table is the canonical happy-path 0")
	assert.Equal(t, 0, count, "no rows = 0 (NOT an error)")
}

// TestCountPersistedSince_CURRENT_TIMESTAMP_Default is the
// godlike/07 fail-closed format-compatibility test (code-reviewer
// MUST-FIX from the Commit 2 follow-up). The media_assets.created_at
// column has `DEFAULT CURRENT_TIMESTAMP` which the Mattn driver
// fills as "YYYY-MM-DD HH:MM:SS" (space-separated, no T, no Z).
// A naive string comparison `created_at >= '2026-07-12T10:00:00Z'`
// would falsely compare `space`-formatted rows as "less than" the
// `T`-formatted input, so the helper would return 0 for production
// rows even when real assets exist — breaking Rule 2 of the
// state machine. The fix: coerce BOTH sides through SQLite's
// `datetime()` function (format-agnostic). This test pins the
// fix: a row inserted without explicit created_at (uses the
// column's CURRENT_TIMESTAMP default) MUST be counted.
func TestCountPersistedSince_CURRENT_TIMESTAMP_Default(t *testing.T) {
	db, cleanup := newTestDBForCountPersistedSince(t)
	defer cleanup()
	repo := NewClipsRepository(db, zap.NewNop())
	ctx := context.Background()

	// Insert 3 rows WITHOUT specifying created_at — uses the
	// column's CURRENT_TIMESTAMP default (space format).
	now := time.Now().UTC()
	for i, id := range []string{"cur1", "cur2", "cur3"} {
		_ = i
		_, err := db.Exec(`INSERT INTO media_assets (id, source) VALUES (?, ?)`, id, "artlist")
		require.NoError(t, err, "insert row %s with CURRENT_TIMESTAMP default", id)
	}

	// Query with since=now-1h: all 3 rows must be counted.
	since := now.Add(-1 * time.Hour)
	count, err := repo.CountPersistedSince(ctx, "artlist", since)
	require.NoError(t, err, "format-agnostic datetime() coercion must count CURRENT_TIMESTAMP-formatted rows")
	assert.Equal(t, 3, count,
		"the 3 rows inserted with CURRENT_TIMESTAMP default MUST be counted (verifies the format-agnostic datetime() coercion; raw string comparison would fail this assertion)")

	// Sanity check: the rows really are CURRENT_TIMESTAMP-formatted
	// (not time.RFC3339) by reading the column back.
	var curFormat string
	require.NoError(t, db.QueryRow(`SELECT created_at FROM media_assets WHERE id=?`, "cur1").Scan(&curFormat))
	assert.Contains(t, curFormat, " ",
		"sanity: the canonical media_assets.created_at default is space-formatted (not T-formatted); this is what production rows look like")
	assert.NotContains(t, curFormat, "T",
		"sanity: production rows should NOT contain 'T' (CURRENT_TIMESTAMP uses space separator)")

	// Cross-format comparison: a future row inserted with explicit
	// time.RFC3339 format must also be counted alongside the
	// CURRENT_TIMESTAMP-formatted rows. This pins the helper's
	// format-agnostic guarantee end-to-end.
	insertMediaAssetWithCreatedAt(t, db, "rfc1", "artlist", time.Now().UTC().Format(time.RFC3339))
	countMixed, err := repo.CountPersistedSince(ctx, "artlist", since)
	require.NoError(t, err)
	assert.Equal(t, 4, countMixed,
		"mixed-format table (CURRENT_TIMESTAMP + RFC3339 rows) must be counted together; this is the production-realistic case")
}

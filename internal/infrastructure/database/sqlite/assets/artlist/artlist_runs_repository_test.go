// Package assets — artlist_runs_repository_test.go pins the canonical
// RunRecord ↔ artlist_runs schema round-trip contract for
// PR-ARTLIST-PERSIST-FIX (godlike/06 SSOT).
//
// We use a real in-memory mattn/go-sqlite3 database so the SQL
// INSERT OR REPLACE semantics + DEFAULT datetime('now') clauses + the
// status TEXT NOT NULL constraint are honoured verbatim against the
// migration schema in migrations/sqlite/001_velox_core.sql:46-59.
//
// sqlmock-style stubs would not exercise the constraint failure or the
// DEFAULT-clock behaviour; we want both.
package artlist

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newArtlistRunsTestDB opens a fresh in-memory SQLite + applies the
// exact artlist_runs CREATE TABLE statement from
// migrations/sqlite/001_velox_core.sql:46-59. The schema is mirrored
// verbatim (column names, types, NOT NULL constraints, DEFAULTs) so the
// repo's INSERT OR REPLACE + ErrorMessage / nullable-text / status
// contracts are exercised against the real SQLite semantics.
func newArtlistRunsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE artlist_runs (
			id TEXT PRIMARY KEY,
			term TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			root_folder_id TEXT,
			tag_folder_id TEXT,
			requested_count INTEGER DEFAULT 0,
			found_count INTEGER DEFAULT 0,
			processed_count INTEGER DEFAULT 0,
			skipped_count INTEGER DEFAULT 0,
			failed_count INTEGER DEFAULT 0,
			error_message TEXT,
			created_at TEXT DEFAULT (datetime('now')),
			updated_at TEXT DEFAULT (datetime('now'))
		)
	`)
	require.NoError(t, err, "create artlist_runs (mirrors migration 001_velox_core.sql:46-59)")
	return db
}

// selectRunRecordByID fetches the artlist_runs row keyed by id and
// scans all 11 writer-managed columns + the 2 DB-side DEFAULT clock
// columns into Go values. Used as the SELECT half of every round-trip
// subtest so the assert side does not depend on the Repo exposing a
// read API (godlike/06 SSOT: writing is the only canonical surface —
// /api/artlist/stats reads via a legacy counters surface).
func selectRunRecordByID(t *testing.T, db *sql.DB, runID string) (
	id, term, status, rootFolderID, tagFolderID string,
	requestedN, foundN, processedN, skippedN, failedN int,
	errorMessage, createdAt, updatedAt string,
	ok bool,
) {
	t.Helper()
	row := db.QueryRow(`
		SELECT id, term, status, root_folder_id, tag_folder_id,
			requested_count, found_count, processed_count, skipped_count, failed_count,
			error_message, created_at, updated_at
		FROM artlist_runs WHERE id = ?
	`, runID)
	if err := row.Scan(
		&id, &term, &status, &rootFolderID, &tagFolderID,
		&requestedN, &foundN, &processedN, &skippedN, &failedN,
		&errorMessage, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return "", "", "", "", "", 0, 0, 0, 0, 0, "", "", "", false
		}
		t.Fatalf("SELECT artlist_runs WHERE id=%q: %v", runID, err)
	}
	return id, term, status, rootFolderID, tagFolderID,
		requestedN, foundN, processedN, skippedN, failedN,
		errorMessage, createdAt, updatedAt, true
}

// TestArtlistRunsRepository_RecordRoundTrip_FullFields writes a
// RunRecord with every one of the 11 writer-managed fields populated
// with a non-empty distinct value, then asserts every column
// round-trips back into the SELECT projection. This pins the
// RunRecord.RunID → column.id AND every other Go field → matching
// SQLite column mapping.
func TestArtlistRunsRepository_RecordRoundTrip_FullFields(t *testing.T) {
	db := newArtlistRunsTestDB(t)
	ctx := context.Background()

	repo, err := NewArtlistRunsRepository(db, zap.NewNop())
	require.NoError(t, err, "constructor MUST accept valid db")

	rec := RunRecord{
		RunID:        "rt-full-001",
		Term:         "sunset timelapse",
		Status:       "completed",
		RootFolderID: "drive-folder-root-001",
		TagFolderID:  "drive-folder-tag-001",
		RequestedN:   42,
		FoundN:       38,
		ProcessedN:   33,
		SkippedN:     5,
		FailedN:      0,
		ErrorMessage: "",
	}
	require.NoError(t, repo.Record(ctx, rec), "Record MUST succeed for a valid RunRecord")

	id, term, status, rootF, tagF, reqN, foundN, procN, skipN, failN, errMsg, cAt, uAt, ok := selectRunRecordByID(t, db, rec.RunID)
	require.True(t, ok, "artlist_runs row MUST exist after Record(...); got ErrNoRows")

	assert.Equal(t, rec.RunID, id, "id round-trips")
	assert.Equal(t, rec.Term, term, "term round-trips")
	assert.Equal(t, rec.Status, status, "status round-trips")
	assert.Equal(t, rec.RootFolderID, rootF, "root_folder_id round-trips")
	assert.Equal(t, rec.TagFolderID, tagF, "tag_folder_id round-trips")
	assert.Equal(t, rec.RequestedN, reqN, "requested_count round-trips")
	assert.Equal(t, rec.FoundN, foundN, "found_count round-trips")
	assert.Equal(t, rec.ProcessedN, procN, "processed_count round-trips")
	assert.Equal(t, rec.SkippedN, skipN, "skipped_count round-trips")
	assert.Equal(t, rec.FailedN, failN, "failed_count round-trips")
	assert.Equal(t, rec.ErrorMessage, errMsg, "error_message round-trips (empty stays empty)")

	// DB-side DEFAULT clock — not part of the 11 writer-managed
	// columns but MUST be populated by SQLite's datetime('now').
	assert.NotEmpty(t, cAt, "created_at MUST be populated by DEFAULT datetime('now') clause")
	assert.NotEmpty(t, uAt, "updated_at MUST be populated by DEFAULT datetime('now') clause")
}

// TestArtlistRunsRepository_RecordRoundTrip_NullableEmptyText pins
// the empty-string convention for nullable TEXT columns
// (root_folder_id, tag_folder_id, error_message). The schema permits
// NULL but the canonical writer uses empty strings — when the column
// is read back, both NULL and "" collapse to "" (std
// database/sql string scan behaviour). This test asserts the
// "writer uses ”, reader sees ”" convention so a future drift to
// NULL-aware sql.NullString types would surface as a failure here.
func TestArtlistRunsRepository_RecordRoundTrip_NullableEmptyText(t *testing.T) {
	db := newArtlistRunsTestDB(t)
	ctx := context.Background()

	repo, err := NewArtlistRunsRepository(db, zap.NewNop())
	require.NoError(t, err, "constructor MUST accept valid db")

	rec := RunRecord{
		RunID:        "rt-empty-001",
		Term:         "term-without-folders",
		Status:       "completed",
		RootFolderID: "", // nullable; canonical = empty string
		TagFolderID:  "", // nullable; canonical = empty string
		RequestedN:   0,
		FoundN:       0,
		ProcessedN:   0,
		SkippedN:     0,
		FailedN:      0,
		ErrorMessage: "", // nullable; canonical = empty string
	}
	require.NoError(t, repo.Record(ctx, rec), "Record MUST succeed with empty nullable TEXT fields")

	id, term, status, rootF, tagF, _, _, _, _, _, errMsg, _, _, ok := selectRunRecordByID(t, db, rec.RunID)
	require.True(t, ok, "artlist_runs row MUST exist after Record(...)")
	assert.Equal(t, rec.RunID, id)
	assert.Equal(t, rec.Term, term)
	assert.Equal(t, rec.Status, status)
	assert.Equal(t, "", rootF, "root_folder_id empty-string convention round-trips as ''")
	assert.Equal(t, "", tagF, "tag_folder_id empty-string convention round-trips as ''")
	assert.Equal(t, "", errMsg, "error_message empty-string convention round-trips as ''")
}

// TestArtlistRunsRepository_RecordRejectsEmptyStatus pins the
// godlike/07 fail-fast contract: a RunRecord with Status="" is
// rejected at the application seam BEFORE the SQL Exec wraps the
// SQLite NOT NULL constraint violation. The diagnostic names the
// canonical schema column so operators can branch on intent.
func TestArtlistRunsRepository_RecordRejectsEmptyStatus(t *testing.T) {
	db := newArtlistRunsTestDB(t)
	ctx := context.Background()

	repo, err := NewArtlistRunsRepository(db, zap.NewNop())
	require.NoError(t, err)

	rec := RunRecord{
		RunID:  "rt-empty-status-001",
		Term:   "x",
		Status: "", // VIOLATES status TEXT NOT NULL DEFAULT 'queued'
	}
	err = repo.Record(ctx, rec)
	require.Error(t, err, "Record MUST reject empty Status (status TEXT NOT NULL DEFAULT 'queued')")
	assert.Contains(t, err.Error(), "Status is required",
		"Error diagnostic MUST reference Status explicitly so callers branch on intent")
	assert.Contains(t, err.Error(), "001_velox_core.sql",
		"Error diagnostic MUST cite the migration anchor so operators can locate the constraint")

	// The row MUST NOT exist — the repo rejects BEFORE the SQL Exec.
	_, _, _, _, _, _, _, _, _, _, _, _, _, ok := selectRunRecordByID(t, db, rec.RunID)
	assert.False(t, ok, "no artlist_runs row MUST exist for a rejected RunRecord")
}

// TestArtlistRunsRepository_RecordRejectsEmptyRunID pins the second
// fail-closed gate: a RunRecord with RunID="" is rejected because
// every artlist_runs row MUST be keyed on a non-empty id PK
// (godlike/06 SSOT — the RunID is the canonical aggregation key
// from /api/artlist/run orchestration).
func TestArtlistRunsRepository_RecordRejectsEmptyRunID(t *testing.T) {
	db := newArtlistRunsTestDB(t)
	ctx := context.Background()

	repo, err := NewArtlistRunsRepository(db, zap.NewNop())
	require.NoError(t, err)

	err = repo.Record(ctx, RunRecord{
		RunID:  "",
		Term:   "x",
		Status: "completed",
	})
	require.Error(t, err, "Record MUST reject empty RunID (id TEXT PRIMARY KEY)")
	assert.Contains(t, err.Error(), "RunID is required",
		"Error diagnostic MUST reference RunID explicitly")
}

// TestArtlistRunsRepository_RecordUpsertsOnRunID pins the
// idempotent-retry contract: a second Record call with the same
// RunID replaces the prior row's columns atomically — no duplicate
// rows, no surface change to the reader. This is the single-TX
// "concurrent retry of the same logical run collapses into ONE
// row" guarantee documented in the godoc.
func TestArtlistRunsRepository_RecordUpsertsOnRunID(t *testing.T) {
	db := newArtlistRunsTestDB(t)
	ctx := context.Background()

	repo, err := NewArtlistRunsRepository(db, zap.NewNop())
	require.NoError(t, err)

	// First write: small run.
	rec1 := RunRecord{
		RunID: "rt-upsert-001", Term: "x", Status: "running",
		RequestedN: 10, FoundN: 8, ProcessedN: 4, SkippedN: 0, FailedN: 0,
	}
	require.NoError(t, repo.Record(ctx, rec1), "first Record")

	// Second write: SAME RunID, different counts (retry / completion
	// of the same logical run).
	rec2 := RunRecord{
		RunID: "rt-upsert-001", Term: "x", Status: "completed",
		RequestedN: 10, FoundN: 8, ProcessedN: 8, SkippedN: 0, FailedN: 2,
		ErrorMessage: "transient-dlq-2",
	}
	require.NoError(t, repo.Record(ctx, rec2), "second Record MUST upsert atomically")

	// Exactly ONE row in artlist_runs (not two).
	var rowCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM artlist_runs WHERE id = ?`, rec1.RunID,
	).Scan(&rowCount), "COUNT WHERE id = ? MUST succeed")
	assert.Equal(t, 1, rowCount,
		"INSERT OR REPLACE MUST collapse two writes with same RunID into ONE row")

	// The row reflects the SECOND write (the retry's terminal state).
	_, _, status, _, _, reqN, _, procN, _, failN, errMsg, _, _, ok := selectRunRecordByID(t, db, rec1.RunID)
	require.True(t, ok)
	assert.Equal(t, rec2.Status, status, "Status reflects latest write (retry terminal state)")
	assert.Equal(t, rec2.ProcessedN, procN, "processed_count reflects latest write")
	assert.Equal(t, rec2.FailedN, failN, "failed_count reflects latest write")
	assert.Equal(t, rec2.ErrorMessage, errMsg, "error_message reflects latest write")
	assert.Equal(t, rec2.RequestedN, reqN, "requested_count also reflects latest write")
}

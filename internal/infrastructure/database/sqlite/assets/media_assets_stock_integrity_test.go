// Package assets — media_assets_stock_integrity_test.go
//
// DoD §4 — SQLite integrity assertions for stock rows in media_assets.
// Pins the canonical contract that every stock row committed to
// media_assets (source='stock') has a complete file footprint:
//
//   - source='stock', media_type='video'
//   - file_hash != ”             (content fingerprint populated)
//   - drive_file_id != ”         (uploaded to Drive)
//   - drive_link != ”            (canonical shareable URL)
//   - duration_ms > 0             (when media_type='video')
//   - lifecycle_state='PUBLISHED' rows never have empty
//     file_hash / drive_file_id / drive_link (no orphan SUCCEEDED assets)
//   - file_hash is unique across source='stock' rows (no duplicates)
//
// godlike/06 SSOT: the SELECT query mirrors the canonical runbook probe
// in scripts/stock_pipeline_live_test.sh STEP 5/6 (the CASE WHEN expression
// + substr truncation is preserved verbatim). The schema is the FULL
// 40-column media_assets schema accumulated through migrations 001-160
// (matches the inline schema in search_queries_lifecycle_test.go for
// fidelity — any column drift surfaces immediately as an INSERT error).
//
// The time-window filter `created_at > datetime('now','-30 minutes')`
// from the runbook is dropped because the test uses explicit timestamps
// via seedStockRow (the in-memory SQLite clock is not anchored to the
// host clock at the test layer).
package assets

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canonicalStockSelect mirrors the runbook STEP 5/6 SELECT (CASE WHEN +
// substr expression preserved verbatim). The trailing ORDER BY
// created_at DESC is the runbook's default sort order.
const canonicalStockSelect = `
SELECT id, filename, '' AS folder_id, index_state, lifecycle_state,
       CASE WHEN file_hash     = '' THEN '-' ELSE substr(file_hash,     1, 12) || '...' END,
       CASE WHEN drive_file_id = '' THEN '-' ELSE substr(drive_file_id, 1, 12) || '...' END
FROM media_assets
WHERE source = 'stock'
ORDER BY created_at DESC`

// stockRow mirrors the SELECT projection (7 columns, including the two
// CASE WHEN placeholders that emit '-' for empty and a 12-char-prefix
// + '...' for non-empty).
type stockRow struct {
	ID            string
	Filename      string
	FolderIDAlias string // mirrors the runbook's empty-string slot
	IndexState    string
	Lifecycle     string
	FileHashDisp  string // '-' or truncated-with-...
	DriveIDDisp   string // '-' or truncated-with-...
}

// stockDetailRow carries the companion-query fields (media_type +
// duration_ms + drive_link) for a single stock row. media_type and
// duration_ms are not in the canonical SELECT (runbook STEP 5
// projection); drive_link is not in the canonical SELECT either —
// the runbook checks drive_link separately in STEP 6. We consolidate
// them into a single companion query for efficiency (one round-trip
// per row instead of three).
type stockDetailRow struct {
	MediaType  string
	DurationMS int
	DriveLink  string // raw drive_link column (NOT synthesized)
}

// stockIntegrityReport holds the raw SELECT row + computed violation
// flags derived from the runbook's STEP 6 verification logic.
//
// Per godlike/06 SSOT: HasFileHash / HasDriveFileID / HasDriveLink are
// derived from the CASE WHEN '-' placeholder — this is the canonical
// SSOT signal (matches runbook's `CASE WHEN file_hash=” THEN '<empty>'
// ELSE substr(...) END` rendering).
type stockIntegrityReport struct {
	Row                 stockRow
	HasFileHash         bool
	HasDriveFileID      bool
	HasDriveLink        bool
	IsPublished         bool
	IsVideo             bool
	DurationMS          int
	IsOrphanedPublished bool // PUBLISHED with any of {file_hash, drive_file_id, drive_link} empty
	HasZeroDuration     bool // media_type='video' but duration_ms <= 0
}

// setupStockIntegrityDB creates an in-memory SQLite with the canonical
// 40-column media_assets schema (matches search_queries_lifecycle_test.go
// for fidelity). The DB is auto-closed via t.Cleanup.
//
// SetMaxOpenConns(1) is REQUIRED for in-memory SQLite: with the default
// connection pool each *sql.Conn used by Exec/Query may land on a
// different in-memory database (sqlite ":memory:" creates a separate DB
// per connection unless `cache=shared` is used). Pinning to a single
// connection guarantees that the CREATE TABLE on connection A is visible
// to every subsequent SELECT/INSERT on the same session — without this,
// tests intermittently fail with `no such table: media_assets`.
func setupStockIntegrityDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	db.SetMaxOpenConns(1) // in-memory SQLite is per-connection; pin to one
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT, tags TEXT, tags_norm TEXT,
			embedding_json TEXT, duration_ms INTEGER, url TEXT,
			media_type TEXT NOT NULL DEFAULT '',
			status TEXT,
			local_path TEXT, relative_path TEXT,
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_folder_id TEXT,
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT,
			file_hash TEXT NOT NULL DEFAULT '',
			metadata_json TEXT,
			visual_embedding TEXT, transcript_embedding TEXT,
			created_at TEXT, updated_at TEXT,
			width INTEGER, height INTEGER,
			lifecycle_state TEXT NOT NULL DEFAULT '',
			index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
			index_state_updated_at TEXT NOT NULL DEFAULT '',
			deleted_at TEXT,
			folder_id TEXT, parent_folder_id TEXT, folder_path TEXT, category TEXT,
			group_name TEXT, filename TEXT, error TEXT, thumb_url TEXT, phash TEXT,
			search_text TEXT, scene_type TEXT, quality_score REAL, reuse_count INTEGER, last_used_at TEXT
		)`)
	require.NoError(t, err, "create media_assets schema")
	return db
}

// stockSeed carries the per-test seed values. Only the fields that
// drive integrity checks are exposed; the rest fall back to sensible
// defaults so each sub-test stays compact.
type stockSeed struct {
	ID          string
	Source      string
	MediaType   string
	Lifecycle   string
	CreatedAt   string
	FileHash    string
	DriveFileID string
	DriveLink   string
	DurationMS  int
	LocalPath   string
	Filename    string
}

// seedStockRow inserts a single media_assets row populated with the
// fields that matter for the integrity checks. Defaults make T1_HappyPath
// ergonomic (omit every field that should be valid).
func seedStockRow(t *testing.T, db *sql.DB, s stockSeed) {
	t.Helper()
	if s.ID == "" {
		s.ID = "stock-test-row"
	}
	if s.Source == "" {
		s.Source = "stock"
	}
	if s.MediaType == "" {
		s.MediaType = "video"
	}
	if s.Lifecycle == "" {
		s.Lifecycle = "PUBLISHED"
	}
	if s.CreatedAt == "" {
		s.CreatedAt = "2026-07-19 10:00:00"
	}
	if s.Filename == "" {
		s.Filename = s.ID + ".mp4"
	}
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO media_assets (
			id, source, media_type, lifecycle_state, created_at,
			file_hash, drive_file_id, drive_link, duration_ms,
			local_path, filename
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Source, s.MediaType, s.Lifecycle, s.CreatedAt,
		s.FileHash, s.DriveFileID, s.DriveLink, s.DurationMS,
		s.LocalPath, s.Filename,
	)
	require.NoError(t, err, "seed stock row id=%s", s.ID)
}

// fetchStockReports runs the canonical SELECT + a companion query
// per row (media_type + duration_ms + drive_link), returning one
// integrity report per row.
//
// The canonical SELECT deliberately omits duration_ms and drive_link
// (per runbook STEP 5 projection). We keep the runbook query verbatim
// and pull those fields via a companion query — this preserves the
// SSOT contract: the production probe and the test probe run the same
// STEP 5 SELECT, and we additionally reach for the STEP 6 attribute
// fields (drive_link is checked separately in runbook STEP 6).
func fetchStockReports(t *testing.T, db *sql.DB) []stockIntegrityReport {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), canonicalStockSelect)
	require.NoError(t, err)

	var rawRows []stockRow
	for rows.Next() {
		var r stockRow
		require.NoError(t, rows.Scan(
			&r.ID, &r.Filename, &r.FolderIDAlias, &r.IndexState,
			&r.Lifecycle, &r.FileHashDisp, &r.DriveIDDisp,
		))
		rawRows = append(rawRows, r)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())

	var reports []stockIntegrityReport
	for _, r := range rawRows {
		var d stockDetailRow
		require.NoError(t, db.QueryRowContext(context.Background(),
			`SELECT media_type, duration_ms, drive_link FROM media_assets WHERE id = ?`,
			r.ID,
		).Scan(&d.MediaType, &d.DurationMS, &d.DriveLink))

		reports = append(reports, reportFromRow(r, d))
	}
	return reports
}

// reportFromRow maps the SELECT result + companion fields to the
// computed violation flags. The CASE WHEN placeholder '-' is the
// canonical signal for emptiness (matches the runbook's display).
//
// HasDriveLink reads from the raw drive_link column (NOT synthesized
// from drive_file_id) — production reality is that drive_link and
// drive_file_id are independent columns: drive_file_id is the raw
// Drive file ID, drive_link is the canonical shareable URL. They can
// be inconsistent (one present, one empty). The test asserts each
// independently per DoD §4.
func reportFromRow(r stockRow, d stockDetailRow) stockIntegrityReport {
	rep := stockIntegrityReport{
		Row:            r,
		HasFileHash:    r.FileHashDisp != "-",
		HasDriveFileID: r.DriveIDDisp != "-",
		HasDriveLink:   d.DriveLink != "",
		IsPublished:    r.Lifecycle == "PUBLISHED",
		IsVideo:        d.MediaType == "video",
		DurationMS:     d.DurationMS,
	}
	rep.IsOrphanedPublished = rep.IsPublished &&
		(!rep.HasFileHash || !rep.HasDriveFileID || !rep.HasDriveLink)
	rep.HasZeroDuration = rep.IsVideo && d.DurationMS <= 0
	return rep
}

// countDuplicateHashes returns the number of file_hash values that
// appear more than once across source='stock' rows. Zero is the
// canonical "no duplicates" state.
func countDuplicateHashes(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM (
			SELECT file_hash FROM media_assets
			WHERE source = 'stock' AND file_hash != ''
			GROUP BY file_hash HAVING COUNT(*) > 1
		)
	`).Scan(&n)
	require.NoError(t, err)
	return n
}

// ── T1: Happy path — complete row passes every integrity check ──────
//
// DoD §4 PASS condition: source=stock + media_type=video + file_hash!=”
// + drive_file_id!=” + drive_link!=” + duration coherent + no
// duplicate fingerprints + no orphaned PUBLISHED rows.
func TestStockMediaAssets_Integrity_HappyPath(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:          "stock-happy-1",
		FileHash:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		DriveFileID: "1abcXYZ_drive_file_id_value",
		DriveLink:   "https://drive.google.com/file/d/1abcXYZ_drive_file_id_value/view",
		DurationMS:  4000,
		LocalPath:   "data/tmp/stock_stage_X/source.mp4",
		Filename:    "stock-happy-1.mp4",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1, "expected exactly 1 stock row")
	rep := reports[0]

	assert.True(t, rep.IsPublished, "happy-path: lifecycle_state must be PUBLISHED")
	assert.True(t, rep.IsVideo, "happy-path: media_type must be video")
	assert.True(t, rep.HasFileHash, "happy-path: file_hash must be present")
	assert.True(t, rep.HasDriveFileID, "happy-path: drive_file_id must be present")
	assert.True(t, rep.HasDriveLink, "happy-path: drive_link must be present (read from raw column)")
	assert.False(t, rep.IsOrphanedPublished, "happy-path: PUBLISHED row must NOT be orphaned")
	assert.False(t, rep.HasZeroDuration, "happy-path: video duration must be > 0")
	assert.Equal(t, 4000, rep.DurationMS, "happy-path: duration must round-trip from SELECT")
	assert.Equal(t, 0, countDuplicateHashes(t, db),
		"happy-path: zero duplicate file_hashes across source='stock'")
}

// ── T2: Missing file_hash on PUBLISHED → orphaned flag fires ────────
func TestStockMediaAssets_Integrity_MissingFileHash(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:          "stock-nohash-1",
		FileHash:    "", // violation: file_hash must be present on PUBLISHED rows
		DriveFileID: "1abc_drive_file_id",
		DurationMS:  4000,
		LocalPath:   "data/tmp/source.mp4",
		Filename:    "stock-nohash-1.mp4",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1)
	rep := reports[0]

	assert.False(t, rep.HasFileHash,
		"expected file_hash MISSING (CASE WHEN placeholder '-' must surface)")
	assert.True(t, rep.IsOrphanedPublished,
		"PUBLISHED + missing file_hash must be flagged as orphaned (DoD §4: no SUCCEEDED asset without final file)")
	// Other integrity fields pass — the test isolates the file_hash violation.
	assert.True(t, rep.HasDriveFileID, "drive_file_id is present, no false positive on this field")
	assert.False(t, rep.HasZeroDuration, "duration > 0, no false positive")
}

// ── T3: Missing drive_file_id → orphaned flag fires ─────────────────
func TestStockMediaAssets_Integrity_MissingDriveFileID(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:       "stock-nodrive-1",
		FileHash: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		// DriveFileID: ""  // violation: drive_file_id must be present on PUBLISHED rows
		DurationMS: 4000,
		LocalPath:  "data/tmp/source.mp4",
		Filename:   "stock-nodrive-1.mp4",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1)
	rep := reports[0]

	assert.False(t, rep.HasDriveFileID,
		"expected drive_file_id MISSING (CASE WHEN placeholder '-' must surface)")
	assert.False(t, rep.HasDriveLink,
		"drive_link is an independent column (NOT synthesized) → empty when DriveLink is not seeded")
	assert.True(t, rep.IsOrphanedPublished,
		"PUBLISHED + missing drive_file_id must be flagged as orphaned")
}

// ── T4: Missing drive_link → orphaned flag fires ────────────────────
func TestStockMediaAssets_Integrity_MissingDriveLink(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:          "stock-nolink-1",
		FileHash:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		DriveFileID: "1abc_drive_present",
		// DriveLink: ""  // violation: drive_link must be present on PUBLISHED rows
		DurationMS: 4000,
		LocalPath:  "data/tmp/source.mp4",
		Filename:   "stock-nolink-1.mp4",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1)
	rep := reports[0]

	assert.True(t, rep.HasDriveFileID, "drive_file_id IS present in this seed")
	assert.False(t, rep.HasDriveLink,
		"expected drive_link MISSING — the synthesized web URL would be empty")
	assert.True(t, rep.IsOrphanedPublished,
		"PUBLISHED + missing drive_link must be flagged as orphaned")
}

// ── T5: Zero duration on a video asset → duration check fires ──────
func TestStockMediaAssets_Integrity_ZeroDuration(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:          "stock-zerodur-1",
		FileHash:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		DriveFileID: "1abc_drive",
		DurationMS:  0, // violation: media_type=video but duration_ms=0
		LocalPath:   "data/tmp/source.mp4",
		Filename:    "stock-zerodur-1.mp4",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1)
	rep := reports[0]

	assert.True(t, rep.IsVideo, "media_type must be video for this assertion")
	assert.Equal(t, 0, rep.DurationMS, "duration_ms must round-trip as zero")
	assert.True(t, rep.HasZeroDuration,
		"video asset with duration_ms=0 must be flagged as zero-duration")
}

// ── T6: Duplicate file_hash → fingerprint dedup check fires ────────
func TestStockMediaAssets_Integrity_DuplicateFileHash(t *testing.T) {
	db := setupStockIntegrityDB(t)
	// Two distinct rows with identical file_hash — bit-identical
	// content fingerprint, the canonical duplicate boundary for stock.
	seedStockRow(t, db, stockSeed{
		ID:          "stock-dup-1",
		FileHash:    "duplicate_fingerprint_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DriveFileID: "drive_A_id",
		DurationMS:  4000,
		Filename:    "stock-dup-1.mp4",
	})
	seedStockRow(t, db, stockSeed{
		ID:          "stock-dup-2",
		FileHash:    "duplicate_fingerprint_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DriveFileID: "drive_B_id",
		DurationMS:  4000,
		Filename:    "stock-dup-2.mp4",
	})

	reports := fetchStockReports(t, db)
	assert.Len(t, reports, 2, "expected both duplicate rows in the canonical SELECT")
	assert.Equal(t, 1, countDuplicateHashes(t, db),
		"expected exactly 1 duplicate file_hash group (DoD §4: no duplicates per fingerprint)")
}

// ── T7: Multiple violations on a single row → all flags fire ───────
//
// Sanity test that reportFromRow accumulates flags rather than
// fail-fast on first violation — a multi-broken PUBLISHED asset
// must surface ALL violations in a single pass.
func TestStockMediaAssets_Integrity_MultipleViolations(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:          "stock-multi-1",
		FileHash:    "", // violation 1
		DriveFileID: "", // violation 2
		DurationMS:  0,  // violation 3
		Lifecycle:   "PUBLISHED",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1)
	rep := reports[0]

	assert.False(t, rep.HasFileHash, "violation 1: missing file_hash")
	assert.False(t, rep.HasDriveFileID, "violation 2: missing drive_file_id")
	assert.False(t, rep.HasDriveLink, "violation propagated: missing drive_link (read from raw column)")
	assert.True(t, rep.HasZeroDuration, "violation 3: zero duration on video asset")
	assert.True(t, rep.IsOrphanedPublished, "violation: PUBLISHED row with empty file footprint is orphaned")
}

// ── T8: Non-stock rows are NOT touched by the integrity filter ─────
//
// The canonical SELECT filters source='stock' (runbook STEP 5/6).
// A non-stock row with a missing file_hash must NOT appear in the
// report (no false positive on the integrity check).
func TestStockMediaAssets_Integrity_NonStockRowIsIgnored(t *testing.T) {
	db := setupStockIntegrityDB(t)
	seedStockRow(t, db, stockSeed{
		ID:          "youtube-row-1",
		Source:      "youtube",
		FileHash:    "", // would be orphaned IF the row were stock — but it's not.
		DriveFileID: "",
		DurationMS:  0,
		Filename:    "youtube-row-1.mp4",
	})
	// Plus one valid stock row to make the filter boundary explicit.
	seedStockRow(t, db, stockSeed{
		ID:          "stock-good-1",
		FileHash:    "valid_hash_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DriveFileID: "1valid",
		DurationMS:  4000,
		Filename:    "stock-good-1.mp4",
	})

	reports := fetchStockReports(t, db)
	require.Len(t, reports, 1, "only source='stock' rows must appear in the integrity report")
	assert.Equal(t, "stock-good-1", reports[0].Row.ID,
		"canonical SELECT must filter source='stock' (godlike/06 SSOT)")
}

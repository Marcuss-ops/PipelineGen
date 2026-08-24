package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestMigrations_092_093_FreshDB verifies:
//  1. Round 1 (clean DB):  RunMigrationsOnDB applies migrations 092 + 093
//     and produces the expected tables + indexes from scratch.
//  2. Round 2 (idempotency): re-running RunMigrationsOnDB on the same DB
//     is a clean no-op (ledger unchanged, tables unchanged, no errors).
//
// The two migrations under test:
//
//	092_create_outbox_events.sql   — outbox_events table (17 cols) +
//	                                  UNIQUE INDEX ux_outbox_events_event_key +
//	                                  INDEX idx_outbox_events_status_next_attempt
//	093_create_clip_folders.sql    — clip_folders table (19 cols) +
//	                                  INDEX idx_clip_folders_search_key
//
// Both migrations are scope = "all" (no `-- database:` header), so they
// apply to BOTH primary and observability targets. Round 1 uses targetDB="primary";
// a parallel subtest verifies the same outcome for "observability" to lock in
// the scope-correctness contract.
//
// Mirrors the pattern of TestMigrations_Smoke (ApplyFirstTime + IdempotencySecondApply)
// but focuses on the two migrations most relevant to the 091-redundant drift fix.
func TestMigrations_092_093_FreshDB(t *testing.T) {
	const migrationsRelPath = "../../../migrations/sqlite"
	migrationsDir, err := filepath.Abs(migrationsRelPath)
	require.NoError(t, err, "resolve migrations dir")
	migrationsDir = filepath.Clean(migrationsDir)

	// Scope parameter under test — both 092 + 093 are scope=all, so both
	// "primary" and "observability" should produce identical outcomes.
	scopes := []string{"primary", "observability"}

	for _, targetDB := range scopes {
		targetDB := targetDB
		t.Run("scope="+targetDB, func(t *testing.T) {
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "fresh.sqlite")

			t.Run("round1_apply_first_time", func(t *testing.T) {
				log := zaptest.NewLogger(t)
				err := RunMigrationsOnDB(dbPath, log, migrationsDir, targetDB)
				require.NoError(t, err, "first RunMigrationsOnDB must succeed on fresh DB")

				db, err := sql.Open("sqlite3", dbPath+"?_mode=ro")
				require.NoError(t, err)
				defer db.Close()

				// 1. Ledger: 092 + 093 must be in schema_migrations with non-empty checksums.
				var m092Checksum, m093Checksum string
				err = db.QueryRow(
					`SELECT checksum FROM schema_migrations WHERE version = 092`,
				).Scan(&m092Checksum)
				require.NoError(t, err, "migration 092 must be in schema_migrations ledger")
				require.NotEmpty(t, m092Checksum, "migration 092 checksum must be non-empty")

				err = db.QueryRow(
					`SELECT checksum FROM schema_migrations WHERE version = 093`,
				).Scan(&m093Checksum)
				require.NoError(t, err, "migration 093 must be in schema_migrations ledger")
				require.NotEmpty(t, m093Checksum, "migration 093 checksum must be non-empty")

				// 2. outbox_events table must exist and have the canonical 17 columns
				//    the application code scans into outboxevents.Event.
				outboxCols := mustReadColumnNames(t, db, "outbox_events")
				require.Equal(t,
					[]string{
						"id", "event_type", "aggregate_id", "aggregate_type",
						"payload_json", "event_key", "status", "attempt_count",
						"max_attempts", "last_error", "next_attempt_at", "worker_id",
						"lease_id", "lease_expiry", "completed_at", "created_at",
						"updated_at", "priority", // migration 186 appends priority
					},
					outboxCols,
					"outbox_events column order MUST match canonical order in 092 (Repository.Enqueue projection)",
				)

				// 3. outbox_events declares one UNIQUE INDEX + one composite INDEX
				//    in 092. The INTEGER PRIMARY KEY does NOT generate a
				//    sqlite_autoindex_* entry (SQLite stores INTEGER PRIMARY KEY
				//    cols as the rowid), and explicit UNIQUE INDEX names do not
				//    get auto-renamed — they keep their declared name.
				outboxIndexes := mustReadIndexNames(t, db, "outbox_events")
				require.Contains(t, outboxIndexes, "ux_outbox_events_event_key",
					"unique index on outbox_events.event_key is REQUIRED for ON CONFLICT DO NOTHING in Repository.Enqueue")
				require.Contains(t, outboxIndexes, "idx_outbox_events_status_next_attempt",
					"composite (status, next_attempt_at, id) index from 092 must exist for ClaimNext performance")

				// 4. clip_folders table exists with 19 columns, in the canonical
				//    application-order used by 093.
				clipCols := mustReadColumnNames(t, db, "clip_folders")
				expectedClipCols := []string{
					"id", "source", "source_url", "video_id", "folder_id",
					"folder_path", "local_folder_path", "group_name",
					"manifest_txt_path", "manifest_json_path", "clip_count",
					"processed_count", "failed_count", "skipped_count",
					"last_error", "metadata", "created_at", "updated_at",
					"search_key",
				}
				require.Equal(t, expectedClipCols, clipCols,
					"clip_folders column order MUST match 093 declaration")

				// 5. clip_folders has its declared search_key index.
				clipIndexes := mustReadIndexNames(t, db, "clip_folders")
				require.Contains(t, clipIndexes, "idx_clip_folders_search_key",
					"clip_folders.search_key index from 093 must exist")

				// 6. schema_migrations ledger row count sanity: should include
				//    every in-scope migration from 001 through 093 (plus any
				//    later in-scope migrations). Hard floor: >= 93 if every
				//    migration through to 093 is in scope for targetDB.
				var ledgerCount int64
				err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&ledgerCount)
				require.NoError(t, err)
				require.GreaterOrEqual(t, ledgerCount, int64(93),
					"fresh-DB ledger must include every in-scope migration from 001 through 093")
			})

			// Snapshot the ledger + table fingerprints AFTER round 1 so round 2
			// can compare against a known-good baseline. Open a single read-only
			// connection that lives for the lifetime of the scope — the
			// WAL-mode DB tolerates a long-lived reader so long as writers
			// (round-2 RunMigrationsOnDB) reconcile at commit time.
			round1RO := openReadOnly(t, dbPath)
			round1OutboxCols := mustReadColumnNames(t, round1RO, "outbox_events")
			round1ClipCols := mustReadColumnNames(t, round1RO, "clip_folders")
			round1Ledger092, _ := mustReadChecksum(t, round1RO, 92)
			round1Ledger093, _ := mustReadChecksum(t, round1RO, 93)
			t.Cleanup(func() { _ = round1RO.Close() })

			t.Run("round2_idempotency", func(t *testing.T) {
				log := zaptest.NewLogger(t)
				err := RunMigrationsOnDB(dbPath, log, migrationsDir, targetDB)
				require.NoError(t, err,
					"second RunMigrationsOnDB must succeed as a clean no-op (idempotency contract)")

				// Open a fresh RO handle for round-2 assertions to avoid racing
				// on the in-scope round1RO connection.
				round2RO := openReadOnly(t, dbPath)
				defer round2RO.Close()

				// Ledger checksums MUST be byte-identical → the runner recognizes
				// 092 + 093 as already-applied and does not re-apply or rewrite.
				ledger092, _ := mustReadChecksum(t, round2RO, 92)
				require.Equal(t, round1Ledger092, ledger092,
					"checksum for 092 must NOT change after second apply (ledger invariant)")

				ledger093, _ := mustReadChecksum(t, round2RO, 93)
				require.Equal(t, round1Ledger093, ledger093,
					"checksum for 093 must NOT change after second apply (ledger invariant)")

				// Schema fingerprint must NOT change.
				outboxCols2 := mustReadColumnNames(t, round2RO, "outbox_events")
				require.Equal(t, round1OutboxCols, outboxCols2,
					"outbox_events columns must NOT change after second apply")

				clipCols2 := mustReadColumnNames(t, round2RO, "clip_folders")
				require.Equal(t, round1ClipCols, clipCols2,
					"clip_folders columns must NOT change after second apply")

				t.Logf("round-2 idempotency verified for scope=%s: ledger + schema fingerprints byte-identical to round-1 baseline", targetDB)
			})
		})
	}
}

// mustReadColumnNames returns the column names of `tbl` in declaration
// order (ascending `cid`). Used as a schema fingerprint.
func mustReadColumnNames(t *testing.T, db *sql.DB, tbl string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", tbl)
	require.NoError(t, err, "PRAGMA table_info(%s)", tbl)
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		cols = append(cols, name)
	}
	require.NoError(t, rows.Err())
	return cols
}

// mustReadIndexNames returns the user-defined index names on `tbl`,
// deduped and sorted. Explicit CREATE [UNIQUE] INDEX statements in
// 092 + 093 keep their declared name on disk (SQLite does not rename
// them via sqlite_autoindex_* — that prefix is reserved for UNIQUE
// column *constraints* declared inline in CREATE TABLE … `col UNIQUE`,
// which 092 + 093 do not use).
func mustReadIndexNames(t *testing.T, db *sql.DB, tbl string) []string {
	t.Helper()
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? ORDER BY name ASC`,
		tbl,
	)
	require.NoError(t, err, "read indexes for %s", tbl)
	defer rows.Close()
	seen := make(map[string]struct{}, 4)
	out := make([]string, 0, 4)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	require.NoError(t, rows.Err())
	return out
}

// openReadOnly returns a single shared read-only connection to dbPath.
// WAL-mode SQLite tolerates one reader and one writer concurrently; the
// caller is responsible for closing via t.Cleanup.
func openReadOnly(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath+"?_mode=ro")
	require.NoError(t, err)
	return db
}

// mustReadChecksum reads a single checksum from the schema_migrations ledger.
// Second return reports whether the row existed at all.
func mustReadChecksum(t *testing.T, db *sql.DB, version int) (string, bool) {
	t.Helper()
	var checksum string
	err := db.QueryRow(
		`SELECT checksum FROM schema_migrations WHERE version = ?`, version,
	).Scan(&checksum)
	if err == sql.ErrNoRows {
		return "", false
	}
	require.NoError(t, err, "read ledger row for version %d", version)
	return checksum, true
}

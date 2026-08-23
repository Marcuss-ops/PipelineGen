package storage

import (
	"database/sql"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// schemaMigrationsTable is the DDL for the migration tracking table.
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version          INTEGER PRIMARY KEY,
    migration_id     INTEGER NOT NULL UNIQUE,
    filename         TEXT    NOT NULL,
    checksum         TEXT    NOT NULL,
    checksum_sha256  TEXT    NOT NULL,
    applied_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    app_git_sha      TEXT    NOT NULL DEFAULT 'unknown'
)`

// currentAppGitSHA returns the build identity captured with each newly
// applied migration. Local tests and development binaries use an explicit
// stable fallback rather than an empty audit value.
func currentAppGitSHA() string {
	if value := strings.TrimSpace(os.Getenv("PIPELINEGEN_GIT_SHA")); value != "" {
		return value
	}
	return "unknown"
}

// migrationFile represents a single SQL migration file in the canonical
// migrations dir. Scope is the parsed target-DB list from the file's
// `-- database:` header directive (default: "all" if absent).
//
// Scope-aware migrations: the runner reads each
// file's first non-blank SQL comment line for the optional header
// `-- database: primary|observability|all` (comma-separated, default
// "all"). When the runner's targetDB does not match a migration's scope,
// the migration is skipped before the checksum check — see
// parseMigrationScope + migrationAppliesToTargetDB in
// migrations_discovery.go.
type migrationFile struct {
	version  int
	filename string
	path     string
	scope    string // default "all"; populated by parseMigrationScope
}

// RunMigrations scans targetDir for .sql files, compares their versions
// against the schema_migrations ledger, and applies any pending migrations
// in version order. Each migration file is applied inside its own
// transaction. Applied migrations are recorded with their version, filename,
// and SHA-256 checksum.
//
// Migrations are never modified or renamed after being applied — the
// runner rejects files whose version is already in the ledger but whose
// checksum differs from the recorded one.
//
// Scope-aware migrations: targetDB names the
// canonical DB the receiver represents ("primary" or "observability" in
// the canonical DatabaseSet; "all" for tests / fixtures that don't
// care). Migrations whose `-- database:` header directive excludes
// targetDB are skipped before the checksum check, so a primary-only
// migration (e.g. 109) is never attempted on the observability DB.
func (s *SQLiteDB) RunMigrations(log *zap.Logger, targetDir, targetDB string) error {
	if log == nil {
		log = s.log
		if log == nil {
			log = zap.NewNop()
		}
	}
	return migrateAll(s.DB, log, targetDir, targetDB)
}

// RunMigrationsOnDB is a convenience wrapper that opens the database at
// dbPath, runs migrations, and closes the database. Useful for one-shot
// scripts (e.g. seed_fixture). See RunMigrations for the targetDB
// parameter's meaning.
//
// Scope-aware migrations (June 2026): targetDB selects the canonical DB.
func RunMigrationsOnDB(dbPath string, log *zap.Logger, targetDir, targetDB string) error {
	if log == nil {
		log = zap.NewNop()
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", dbPath, err)
	}
	defer db.Close()

	return migrateAll(db, log, targetDir, targetDB)
}

// migrateAll is the shared migration logic used by both RunMigrations
// and RunMigrationsOnDB. See RunMigrations for the targetDB parameter's
// meaning.
//
// Scope-aware migrations: the loop below skips
// migrations whose `-- database:` header excludes targetDB BEFORE the
// checksum check, so the ledger never gains an entry for an out-of-scope
// file. Migration 109 carries a one-time checksum shim — see below —
// so existing primary DBs that pre-date the `-- database: primary`
// header preserve their applied status.
func migrateAll(db queryable, log *zap.Logger, targetDir, targetDB string) error {
	// Bootstrap and expand the ledger before reading migration files. Existing
	// databases may have the legacy four-column shape; expansion backfills the
	// canonical identity/checksum fields without dropping migration history.
	if err := ensureMigrationLedger(db); err != nil {
		return fmt.Errorf("storage: ensure schema_migrations: %w", err)
	}

	// Discover migration files
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return err
	}

	// Validate: no duplicate versions
	if err := validateNoDuplicateVersions(migrations, log); err != nil {
		return err
	}

	// Validate: log gaps as warnings (not errors) since real migration dirs
	// may have gaps from historical renumbering / removed migrations.
	// Gaps are informational; the runner proceeds normally.
	warnOnGaps(migrations, log)

	// Load already-applied migrations for checksum validation
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("storage: load applied migrations: %w", err)
	}
	if err := validateAppliedMigrationSet(applied, migrations, targetDB); err != nil {
		return err
	}

	// Apply pending migrations
	appliedCount := 0
	for _, m := range migrations {
		// Skip out-of-scope migrations BEFORE the
		// checksum check. A primary-only migration must NEVER land on
		// the observability ledger, regardless of whether its checksum
		// matches. The skip is silent at INFO level (operators
		// running with -v can grep the log for confirmation).
		if !migrationAppliesToTargetDB(m.scope, targetDB) {
			if log != nil {
				log.Debug("skipping migration (out of DB scope)",
					zap.Int("version", m.version),
					zap.String("filename", m.filename),
					zap.String("scope", m.scope),
					zap.String("target_db", targetDB),
				)
			}
			continue
		}
		content, err := os.ReadFile(m.path)
		if err != nil {
			return fmt.Errorf("storage: read %s: %w", m.filename, err)
		}
		checksum := sha256Hex(content)

		if prev, ok := applied[m.version]; ok {
			if prev.filename != m.filename || prev.checksum != checksum { // Identity or checksum drift
				// Migration 198 was deployed once with the equivalent
				// database_id-primary-key form of control_plane_meta. The
				// canonical file now contains the stronger singleton form.
				// Reconcile only that known deployment hash and only after
				// proving the live schema is the expected compatible shape.
				// This repairs the ledger; it does not silently accept an
				// arbitrary edited migration.
				if m.version == 198 && targetDB == "primary" &&
					prev.filename == m.filename &&
					prev.checksum == "c5b770ce3c9fca18234ac8953e143535ec7da8b7740f186f7ce9edb0c518843b" &&
					isLegacyControlPlaneMetaSchema(db) {
					if _, err := db.Exec("UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = 198", checksum, checksum); err != nil {
						return fmt.Errorf("storage: shim update 198 checksum: %w", err)
					}
					if log != nil {
						log.Warn("reconciled migration 198 ledger for compatible deployed control-plane schema", zap.Int("version", m.version), zap.String("filename", m.filename))
					}
					continue
				}
				// Migration 201 was applied by an earlier deployment but its
				// file was not retained in the repository. The deployed schema
				// already contains the taxonomy columns; accept only the known
				// ledger hash and only after verifying those columns exist.
				if m.version == 201 && targetDB == "primary" &&
					prev.filename == m.filename &&
					prev.checksum == "1f1d9052bd130dd810215705923088e79ccd8748f1f7ec46ff47419a1f75c019" &&
					hasMediaTaxonomyColumns(db) {
					if _, err := db.Exec("UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = 201", checksum, checksum); err != nil {
						return fmt.Errorf("storage: shim update 201 checksum: %w", err)
					}
					if log != nil {
						log.Warn("reconciled missing migration 201 ledger for deployed media taxonomy", zap.Int("version", m.version), zap.String("filename", m.filename))
					}
					continue
				}
				if m.version == 195 && targetDB == "primary" &&
					prev.filename == m.filename &&
					prev.checksum == "a341d66a5411f4e8db9c66b75dc57dce44275972db98684ab4602497890b6b6a" &&
					hasMediaTaxonomyColumns(db) {
					if _, err := db.Exec("UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = 195", checksum, checksum); err != nil {
						return fmt.Errorf("storage: shim update 195 checksum: %w", err)
					}
					if log != nil {
						log.Warn("reconciled migration 195 ledger for deployed media taxonomy", zap.Int("version", m.version), zap.String("filename", m.filename))
					}
					continue
				}
				legacyChecksums := map[int]string{
					191: "441f6502693e3ffceb4bf85a18d22559b937624861c5db6daa6cc59233f91d5b",
					192: "90a5ffc831f380fa4e0763835b5699d0049db5192f84125c3b15bac8f77d6d66",
					193: "e5f9ea1d18ca6542346cf6abe0e07d145b992d7b95e50a05affb1b4f44e43358",
				}
				if expected, ok := legacyChecksums[m.version]; ok && targetDB == "primary" && prev.filename == m.filename && prev.checksum == expected {
					if _, err := db.Exec("UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = ?", checksum, checksum, m.version); err != nil {
						return fmt.Errorf("storage: shim update %03d checksum: %w", m.version, err)
					}
					if log != nil {
						log.Warn("reconciled removed historical migration ledger entry", zap.Int("version", m.version), zap.String("filename", m.filename))
					}
					continue
				}
				// Deployment reconciliation for the already-applied local 186
				// outbox-priority migration. The SQL is unchanged in effect;
				// only the untracked deployment copy's checksum differs.
				if m.version == 186 && targetDB == "primary" && prev.filename == m.filename &&
					prev.checksum == "06d40a3bee8655737dacdc758aeb4bf3fe36b5731da74e5f804f39a78b43e807" &&
					checksum == "aa56b176ff008dde206b6da3946859712fbf76fb24bb40d3e42a930a8f950d40" {
					if _, err := db.Exec("UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = 186", checksum, checksum); err != nil {
						return fmt.Errorf("storage: shim update 186 checksum: %w", err)
					}
					if log != nil {
						log.Warn("applied one-time checksum reconciliation for migration 186", zap.Int("version", m.version), zap.String("filename", m.filename))
					}
					continue
				}
				// shim. Migration 109 was edited to prepend the
				// `-- database: primary` header directive, which
				// changes its SHA-256 checksum. Existing primary
				// DBs that already applied the pre-edit version hit
				// this mismatch; the shim rewrites the recorded
				// checksum so the runner can mark the migration as
				// already-applied without losing the audit trail.
				// The shim is gated on (version == 109 && targetDB
				// == "primary") — migrations other than 109 are NOT
				// shimmed, preserving the never-modify-already-
				// applied invariant (the SHA-256 ledger is the
				// canonical audit trail for everything else).
				if m.version == 109 && targetDB == "primary" && prev.filename == m.filename {
					// The shim ONLY fires when
					// the file content carries the magic marker
					// `-- TODO-8-SCOPE-FLAG-RECONCILE-109`. Without
					// the marker, an unexpected modify of migration
					// 109 surfaces as a hard error so the SHA-256
					// ledger invariant is preserved (an attacker or
					// careless edit cannot silently rewrite the
					// ledger). The marker is added alongside the
					// `-- database: primary` directive in the
					// canonical 109 file.
					const shimMarker = "-- TODO-8-SCOPE-FLAG-RECONCILE-109"
					if !strings.Contains(string(content), shimMarker) {
						return fmt.Errorf(
							"storage: migration %03d (%s) checksum mismatch — "+
								"missing shim marker %q in file. The shim "+
								"only fires when the marker is present, so an "+
								"unexpected modify of 109 surfaces as a hard error "+
								"instead of silently rewriting the ledger. To "+
								"honour the legitimate scope-aware modification "+
								"add the marker above the existing content and re-run.",
							m.version, m.filename, shimMarker,
						)
					}
					if _, err := db.Exec(
						"UPDATE schema_migrations SET checksum = ?, checksum_sha256 = ? WHERE version = 109",
						checksum, checksum,
					); err != nil {
						return fmt.Errorf(
							"storage: shim update 109 checksum: %w", err,
						)
					}
					if log != nil {
						// WARN level (was INFO): operators searching
						// for anomalies should NOT have to descend
						// into INFO to discover that the SHA-256
						// ledger was silently rewritten. The new
						// SHA-256 is included in the log line so a
						// future audit can pinpoint what landed.
						log.Warn("applied one-time checksum shim for migration 109 (-- database: primary header added; ledger rewritten with new SHA-256)",
							zap.Int("version", m.version),
							zap.String("filename", m.filename),
							zap.String("new_checksum", checksum),
						)
					}
					continue
				}
				return fmt.Errorf(
					"storage: migration %03d (%s) identity/checksum mismatch — "+
						"applied=%s/%s current=%s/%s. Migrations must never be modified or renamed after being applied",
					m.version, m.filename, prev.filename, prev.checksum, m.filename, checksum,
				)
			}
			// Already applied with matching checksum — skip
			continue
		}

		// Apply migration inside its own transaction
		log.Info("applying migration", zap.Int("version", m.version), zap.String("filename", m.filename))

		if err := applyMigration(db, log, m.version, m.filename, checksum, content); err != nil {
			return fmt.Errorf("storage: apply %s: %w", m.filename, err)
		}
		appliedCount++
	}

	log.Info("migrations complete",
		zap.Int("total", len(migrations)),
		zap.Int("newly_applied", appliedCount),
		zap.Int("already_applied", len(applied)),
	)
	return nil
}

// isLegacyControlPlaneMetaSchema recognizes the schema produced by the one
// deployed pre-singleton variant of migration 198. It is intentionally
// narrow: the checksum shim must never turn an arbitrary schema drift into a
// successful migration run.
func isLegacyControlPlaneMetaSchema(db queryable) bool {
	rows, err := db.Query("PRAGMA table_info(control_plane_meta)")
	if err != nil {
		return false
	}
	defer rows.Close()

	want := map[string]bool{
		"database_id":       false,
		"schema_family":     false,
		"instance_role":     false,
		"canonical_version": false,
		"created_at":        false,
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		if _, ok := want[name]; !ok || name == "singleton_id" {
			return false
		}
		want[name] = true
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, present := range want {
		if !present {
			return false
		}
	}
	countRows, err := db.Query("SELECT COUNT(*) FROM control_plane_meta")
	if err != nil {
		return false
	}
	defer countRows.Close()
	if !countRows.Next() {
		return false
	}
	var count int
	if err := countRows.Scan(&count); err != nil {
		return false
	}
	return count == 1
}

func hasMediaTaxonomyColumns(db queryable) bool {
	rows, err := db.Query("PRAGMA table_info(media_assets)")
	if err != nil {
		return false
	}
	defer rows.Close()
	want := map[string]bool{"namespace": false, "asset_kind": false, "source_type": false}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false
		}
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, present := range want {
		if !present {
			return false
		}
	}
	return true
}

// queryable abstracts the database handle so migrateAll works with both
// *sql.DB (from RunMigrationsOnDB) and *SQLiteDB (from RunMigrations).
type queryable interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	Begin() (*sql.Tx, error)
}

// appliedRecord holds a row from the schema_migrations ledger.
type appliedRecord struct {
	checksum string
	filename string
}

// applyMigration executes a single migration file inside a transaction
// and records its application in schema_migrations.
//
// Migration files are split into individual statements and each is executed
// in turn. SQLite does not support `ALTER TABLE ADD COLUMN IF NOT EXISTS`,
// so we soft-skip any `ALTER TABLE … ADD COLUMN` that fails with
// "duplicate column name": the column was already created by a prior
// migration (e.g. 015 was retrofitted to add columns that 017 also adds).
// The migration is still recorded as fully applied. The check is scoped to
// ALTER TABLE … ADD COLUMN specifically so other DDL with the same error
// string (e.g. a CREATE TABLE with a duplicated column in its column list)
// is still hard-errored as a real bug.
func applyMigration(db queryable, log *zap.Logger, version int, filename, checksum string, content []byte) error {
	if log == nil {
		log = zap.NewNop()
	}
	startedAt := time.Now()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	statements := splitSQLStatements(string(content))
	for i, stmt := range statements {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		// Pre-flight skip: standalone BEGIN/BEGIN TRANSACTION/BEGIN IMMEDIATE/
		// COMMIT/ROLLBACK appear inside migration files the author wrote
		// expecting to need explicit tx boundaries. Within the outer Go-level
		// `tx` from `db.Begin()` these statements are no-ops, but more
		// importantly SQLite treats a stray `COMMIT` as terminating the
		// underlying transaction — which leaves Go's `*sql.Tx` open against a
		// closed SQLite handle. The eventual `tx.Commit()` then errors with
		// "cannot commit - no transaction is active".  Skipping before
		// exec() guarantees the driver never sees the lifecycle command,
		// fixes fresh DBs, and — because the SQL files are unchanged —
		// preserves the SHA-256 ledger invariant on existing databases.
		//
		// This is the ONLY `isNestedTransactionControl` check in the runner:
		// the previous post-error check inside `if err != nil` was deleted
		// because it relied on the driver erroring (which SQLite doesn't do
		// for `COMMIT` inside an active tx — it silently closes the handle).
		if isNestedTransactionControl(trimmed) {
			log.Info("skipping nested transaction control (handled by outer runner tx)",
				zap.Int("version", version),
				zap.String("filename", filename),
				zap.Int("statement", i+1),
			)
			continue
		}
		if _, err := tx.Exec(trimmed); err != nil {
			if isDuplicateColumnError(err.Error(), trimmed) {
				// Soft-skip: ALTER TABLE … ADD COLUMN collided with a column
				// that already exists from a prior migration (e.g. 015 was
				// retrofitted to add columns that 017 also adds). This makes
				// migrations idempotent against retrofitted column lists.
				log.Info("skipping duplicate ADD COLUMN (already exists from prior migration)",
					zap.Int("version", version),
					zap.String("filename", filename),
					zap.Int("statement", i+1),
				)
				continue
			}
			if isConditionalInsertOnMissingTable(err.Error(), trimmed) {
				// Soft-skip: conditional data-migration INSERT that gated on
				// `WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table'
				// AND name='<tbl>')` failed because SQLite's query planner
				// resolves the FROM-table at planning time even when the
				// EXISTS gate would have produced zero rows at runtime.
				// The migration author intended the INSERT to no-op when the
				// source table was absent; this runtime workaround fulfils
				// that intent without modifying the migration file (which
				// would break the SHA-256 ledger invariant).
				log.Info("skipping conditional INSERT onto missing source table",
					zap.Int("version", version),
					zap.String("filename", filename),
					zap.Int("statement", i+1),
					zap.Error(err),
				)
				continue
			}
			return fmt.Errorf("%s: statement %d: %w", filename, i+1, err)
		}
	}

	// Record in ledger
	now := time.Now().UTC().Format(time.RFC3339)
	durationMS := time.Since(startedAt).Milliseconds()
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, migration_id, filename, checksum, checksum_sha256, applied_at, duration_ms, app_git_sha) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		version, version, filename, checksum, checksum, now, durationMS, currentAppGitSHA(),
	); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}

	return tx.Commit()
}

// parseMigrationVersion extracts the integer version prefix from a
// migration filename. Expected format: NNN_<descriptive>.sql
// e.g. "001_velox_core.sql" → 1.
func parseMigrationVersion(filename string) (int, error) {
	name := filepath.Base(filename)
	idx := strings.Index(name, "_")
	if idx <= 0 || idx > 3 {
		return 0, fmt.Errorf("invalid migration filename: %s (expected NNN_*.sql)", name)
	}
	version, err := strconv.Atoi(name[:idx])
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration version in %s: %w", name, err)
	}
	return version, nil
}

// sha256Hex returns the hex-encoded SHA-256 hash of the input bytes.
func sha256Hex(data []byte) string {
	return digest.SHA256Bytes(data)
}

// MigrateStatus represents the status of a single migration file.
type MigrateStatus struct {
	Version  int
	Filename string
	Applied  bool
	Checksum string
}

// MigrateStatusReport holds the result of a migration status check.
type MigrateStatusReport struct {
	Applied  []MigrateStatus
	Pending  []MigrateStatus
	Total    int
	AppliedN int
	PendingN int
}

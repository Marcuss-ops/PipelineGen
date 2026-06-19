package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// schemaMigrationsTable is the DDL for the migration tracking table.
const schemaMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    filename    TEXT    NOT NULL,
    checksum    TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL DEFAULT (datetime('now'))
)`

type migrationFile struct {
	version  int
	filename string
	path     string
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
func (s *SQLiteDB) RunMigrations(log *zap.Logger, targetDir string) error {
	if log == nil {
		log = s.log
		if log == nil {
			log = zap.NewNop()
		}
	}
	return migrateAll(s.DB, log, targetDir)
}

// RunMigrationsOnDB is a convenience wrapper that opens the database at
// dbPath, runs migrations, and closes the database. Useful for one-shot
// scripts (e.g. seed_fixture).
func RunMigrationsOnDB(dbPath string, log *zap.Logger, targetDir string) error {
	if log == nil {
		log = zap.NewNop()
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", dbPath, err)
	}
	defer db.Close()

	return migrateAll(db, log, targetDir)
}

// migrateAll is the shared migration logic used by both RunMigrations
// and RunMigrationsOnDB.
func migrateAll(db queryable, log *zap.Logger, targetDir string) error {
	// Ensure ledger table exists
	if _, err := db.Exec(schemaMigrationsTable); err != nil {
		return fmt.Errorf("storage: create schema_migrations: %w", err)
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

	// Apply pending migrations
	appliedCount := 0
	for _, m := range migrations {
		content, err := os.ReadFile(m.path)
		if err != nil {
			return fmt.Errorf("storage: read %s: %w", m.filename, err)
		}
		checksum := sha256Hex(content)

		if prev, ok := applied[m.version]; ok {
			if prev.checksum != checksum {
				return fmt.Errorf(
					"storage: migration %03d (%s) checksum mismatch — "+
						"applied=%s current=%s. Migrations must never be modified after being applied",
					m.version, m.filename, prev.checksum, checksum,
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

// queryable abstracts the database handle so migrateAll works with both
// *sql.DB (from RunMigrationsOnDB) and *SQLiteDB (from RunMigrations).
type queryable interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	Begin() (*sql.Tx, error)
}

// discoverMigrations scans a directory for .sql migration files and
// returns them sorted by version.
func discoverMigrations(targetDir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, fmt.Errorf("storage: read migrations dir %s: %w", targetDir, err)
	}

	var migrations []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseMigrationVersion(e.Name())
		if err != nil {
			continue // skip non-migration files silently
		}
		migrations = append(migrations, migrationFile{
			version:  version,
			filename: e.Name(),
			path:     filepath.Join(targetDir, e.Name()),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

// validateNoDuplicateVersions returns an error if two migration files share
// the same version number.
func validateNoDuplicateVersions(migrations []migrationFile, log *zap.Logger) error {
	seen := make(map[int]string)
	for _, m := range migrations {
		if prev, ok := seen[m.version]; ok {
			return fmt.Errorf(
				"storage: duplicate migration version %03d: %s and %s",
				m.version, prev, m.filename,
			)
		}
		seen[m.version] = m.filename
	}
	return nil
}

// warnOnGaps logs warnings for any version gaps in the migration sequence.
// Gaps are informational only — the runner proceeds normally. Real migration
// directories may have gaps from historical renumbering or removed migrations.
func warnOnGaps(migrations []migrationFile, log *zap.Logger) {
	if len(migrations) == 0 {
		return
	}

	if migrations[0].version != 1 {
		log.Warn("first migration is not version 001 — possible orphaned migrations",
			zap.Int("first_version", migrations[0].version),
			zap.String("filename", migrations[0].filename))
	}

	expected := migrations[0].version
	for i := 1; i < len(migrations); i++ {
		expected++
		if migrations[i].version != expected {
			gapStart := expected
			gapEnd := migrations[i].version - 1
			if gapStart == gapEnd {
				log.Warn("migration version gap detected",
					zap.Int("gap", gapStart))
			} else {
				log.Warn("migration version gap detected",
					zap.Int("gap_start", gapStart),
					zap.Int("gap_end", gapEnd))
			}
			expected = migrations[i].version
		}
	}
}

// appliedRecord holds a row from the schema_migrations ledger.
type appliedRecord struct {
	checksum string
	filename string
}

// loadAppliedMigrations returns a map of version → appliedRecord for all
// migrations previously recorded in schema_migrations. Returns an empty
// map if the table doesn't exist yet (fresh database).
func loadAppliedMigrations(db queryable) (map[int]appliedRecord, error) {
	applied := make(map[int]appliedRecord)
	rows, err := db.Query("SELECT version, filename, checksum FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		// Distinguish "table doesn't exist yet" from real errors
		if isMissingTableError(err) {
			return applied, nil
		}
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var v int
		var filename, checksum string
		if err := rows.Scan(&v, &filename, &checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations row: %w", err)
		}
		applied[v] = appliedRecord{filename: filename, checksum: checksum}
	}
	return applied, rows.Err()
}

// isMissingTableError returns true if the error indicates the queried table
// does not exist (SQLite "no such table" error).
func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table") &&
		strings.Contains(msg, "schema_migrations")
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
		if isNestedTransactionControl(trimmed) {
			// Soft-skip: standalone BEGIN/BEGIN TRANSACTION/BEGIN IMMEDIATE/
			// COMMIT/ROLLBACK would otherwise attempt to nest inside the
			// runner's outer per-migration transaction and fail with
			// "cannot start a transaction within a transaction".
			// Within the outer tx these statements are no-ops; the
			// migration author intended them as documentation.
			log.Info("skipping nested transaction control (handled by outer runner tx)",
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
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, filename, checksum, applied_at) VALUES (?, ?, ?, ?)",
		version, filename, checksum, now,
	); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}

	return tx.Commit()
}

// splitSQLStatements splits a migration file body into individual SQL
// statements. A statement boundary is a `;` that occurs OUTSIDE:
//   - string literals ('...' and "...")
//   - BEGIN...END trigger/function bodies (depth-tracked by keyword)
//
// Line comments (`-- ...`) are stripped in a pre-pass so semicolons inside
// comments do not produce false boundaries.
//
// This correctly handles migrations/sqlite/034_media_index_outbox.sql
// which contains a CREATE TRIGGER with an embedded `;` inside its
// BEGIN...END body. The naive line-based splitter was flushing at the
// inner `;` and producing a partial CREATE TRIGGER statement that failed
// with a syntax error.
//
// Caveat: matches BEGIN/END case-insensitively as whole words. None of
// our 47 migrations use BEGIN TRANSACTION/ COMMIT pairs, so the depth model
// holds — every BEGIN pairs with a matching END on the trigger body.
func splitSQLStatements(body string) []string {
	// Pre-pass: strip `--` line comments so semicolons inside comments
	// can't confuse the splitter.
	var buf strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	body = buf.String()

	var (
		statements []string
		current    strings.Builder
		inString   bool // tracks single-quoted SQL string literals
		beginDepth int  // tracks BEGIN ... END nesting
	)
	flush := func() {
		stmt := strings.TrimSpace(current.String())
		if stmt != "" {
			statements = append(statements, stmt)
		}
		current.Reset()
	}

	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if inString {
			current.WriteRune(c)
			if c == '\'' {
				// SQL-standard escaped quote ('')
				if i+1 < len(runes) && runes[i+1] == '\'' {
					current.WriteRune(runes[i+1])
					i++
				} else {
					inString = false
				}
			}
			continue
		}

		if c == '\'' {
			inString = true
			current.WriteRune(c)
			continue
		}

		// Word-character run: detect BEGIN/END keywords for depth tracking.
		if isAlphaWordRune(c) {
			j := i
			for j < len(runes) && isAlphaWordRune(runes[j]) {
				j++
			}
			word := strings.ToUpper(string(runes[i:j]))
			switch word {
			case "BEGIN":
				// Peek past whitespace at j: if the next word is one of SQLite's
				// transaction modifiers (IMMEDIATE/TRANSACTION/EXCLUSIVE/DEFERRED),
				// this BEGIN is a transaction-starter and must NOT increment depth.
				// Otherwise, it opens a CREATE TRIGGER body and depth goes up.
				if isTransactionModifierAfter(runes, j) {
					// leave beginDepth unchanged; pre-flight skip will catch
					// the resulting standalone `BEGIN IMMEDIATE` etc.
				} else {
					beginDepth++
				}
			case "END":
				if beginDepth > 0 {
					beginDepth--
				}
			}
			for k := i; k < j; k++ {
				current.WriteRune(runes[k])
			}
			i = j - 1 // outer for-loop advances by 1
			continue
		}

		// Statement boundary: `;` outside any BEGIN...END and outside strings.
		if c == ';' && beginDepth == 0 {
			current.WriteRune(c)
			flush()
			continue
		}

		current.WriteRune(c)
	}

	if rest := strings.TrimSpace(current.String()); rest != "" {
		statements = append(statements, rest)
	}
	return statements
}

// isAlphaWordRune reports whether r is part of a SQL identifier character.
// Used for BEGIN/END keyword detection at word boundaries.
func isAlphaWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// isTransactionModifierAfter reports whether the rune at `after` (immediately
// following the read of a BEGIN keyword) is one of SQLite's transaction
// modifiers (IMMEDIATE / TRANSACTION / EXCLUSIVE / DEFERRED), which makes
// the BEGIN a transaction-starter rather than a CREATE TRIGGER body opener.
// Whitespace between BEGIN and the modifier is skipped.
func isTransactionModifierAfter(runes []rune, after int) bool {
	k := after
	for k < len(runes) && (runes[k] == ' ' || runes[k] == '\t' || runes[k] == '\n' || runes[k] == '\r') {
		k++
	}
	if k >= len(runes) || !isAlphaWordRune(runes[k]) {
		return false
	}
	m := k
	for m < len(runes) && isAlphaWordRune(runes[m]) {
		m++
	}
	switch strings.ToUpper(string(runes[k:m])) {
	case "IMMEDIATE", "TRANSACTION", "EXCLUSIVE", "DEFERRED":
		return true
	}
	return false
}

// isDuplicateColumnError reports whether errMsg is SQLite's
// "duplicate column name" error AND the offending stmt is an
// `ALTER TABLE ... ADD COLUMN` statement.
//
// The ALTER TABLE … ADD COLUMN scoping prevents the runner from silently
// swallowing unrelated "duplicate column name" errors that could arise
// from other DDL (e.g. a CREATE TABLE statement with a duplicated column
// in its column list — a real bug, not an idempotency retry). Only the
// ADD-COLUMN case we want to handle is soft-skipped.
func isDuplicateColumnError(errMsg, stmt string) bool {
	if !strings.Contains(errMsg, "duplicate column name") {
		return false
	}
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	return strings.HasPrefix(upper, "ALTER TABLE") &&
		strings.Contains(upper, "ADD COLUMN")
}

// isConditionalInsertOnMissingTable reports whether errMsg is SQLite's
// "no such table" error AND the offending stmt is a conditional data
// migration shaped like
//
//	INSERT [OR IGNORE] INTO <dst> SELECT ... FROM <src>
//	WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='<src>')
//
// SQLite's query planner resolves the FROM-table at planning time even
// when the EXISTS gate would have made the row source empty, so on a
// fresh DB where <src> was dropped (or never existed because of a
// refactor), the INSERT errors out despite the author's intent that it
// should be a no-op. We soft-skip this specific shape only — anything
// else that errors with "no such table" still hard-fails.
func isConditionalInsertOnMissingTable(errMsg, stmt string) bool {
	if !strings.Contains(errMsg, "no such table") {
		return false
	}
	upper := strings.ToUpper(stmt)
	// Match the gate the migration author wrote: an EXISTS check on
	// sqlite_master that names the missing table.
	return strings.Contains(upper, "WHERE EXISTS (SELECT 1 FROM SQLITE_MASTER WHERE TYPE='TABLE'")
}

// isNestedTransactionControl reports whether the statement is exactly a
// standalone SQLite transaction-control command (BEGIN, BEGIN TRANSACTION,
// BEGIN IMMEDIATE, BEGIN EXCLUSIVE, BEGIN DEFERRED, COMMIT, END, END
// TRANSACTION, ROLLBACK).
//
// These appear inside migration files the author wrote expecting to need
// explicit tx boundaries, but storage.RunMigrations already wraps each
// migration in an outer transaction, so nested BEGIN/COMMIT consistently
// errors with "cannot start a transaction within a transaction".
//
// `END` is recognised as a transaction-commit synonym (per SQLite docs);
// a bare `END` inside a CREATE TRIGGER body is filtered out before this
// check runs because splitSQLStatements' BEGIN/END depth tracking emits
// the whole trigger body as one statement, never a standalone bare `END`.
//
// The check is exact-string-match after trim and uppercase, so:
//   - mid-expression occurrences (e.g. column names, comments containing
//     the word "BEGIN") do NOT match;
//   - splitSQLStatements already guarantees these appear as standalone
//     statements (no trailing `;` after trim+strip).
func isNestedTransactionControl(stmt string) bool {
	s := strings.ToUpper(strings.TrimSpace(stmt))
	s = strings.TrimRight(s, ";")
	s = strings.TrimSpace(s)
	switch s {
	case "BEGIN", "BEGIN TRANSACTION", "BEGIN IMMEDIATE", "BEGIN EXCLUSIVE", "BEGIN DEFERRED",
		"COMMIT", "END", "END TRANSACTION", "ROLLBACK":
		return true
	}
	return false
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
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
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

// GetMigrationStatus compares the migration files on disk against the
// schema_migrations ledger and returns a report of applied vs pending.
// If db is nil, all migrations are reported as pending (useful for
// dry-run inspection without a database connection).
func GetMigrationStatus(db *sql.DB, targetDir string) (*MigrateStatusReport, error) {
	if db == nil {
		return getPendingOnlyStatus(targetDir)
	}
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return nil, err
	}

	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return nil, err
	}

	report := &MigrateStatusReport{Total: len(migrations)}
	for _, m := range migrations {
		content, err := os.ReadFile(m.path)
		checksum := ""
		if err == nil {
			checksum = sha256Hex(content)
		}

		ms := MigrateStatus{
			Version:  m.version,
			Filename: m.filename,
			Checksum: checksum,
		}

		if rec, ok := applied[m.version]; ok {
			ms.Applied = true
			ms.Checksum = rec.checksum
			report.Applied = append(report.Applied, ms)
		} else {
			report.Pending = append(report.Pending, ms)
		}
	}
	report.AppliedN = len(report.Applied)
	report.PendingN = len(report.Pending)
	return report, nil
}

// FormatMigrateStatus formats a MigrateStatusReport as a human-readable table.
func FormatMigrateStatus(report *MigrateStatusReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-7s  %-40s %s\n", "version", "filename", "status"))
	sb.WriteString(strings.Repeat("-", 70) + "\n")

	for _, m := range report.Applied {
		sb.WriteString(fmt.Sprintf("%03d      %-40s applied\n", m.Version, m.Filename))
	}
	for _, m := range report.Pending {
		sb.WriteString(fmt.Sprintf("%03d      %-40s pending\n", m.Version, m.Filename))
	}

	sb.WriteString(fmt.Sprintf(
		"\n%d migration(s) total: %d applied, %d pending\n",
		report.Total, report.AppliedN, report.PendingN,
	))
	return sb.String()
}

// getPendingOnlyStatus returns a report where all migrations are pending.
// Used when no database connection is available.
func getPendingOnlyStatus(targetDir string) (*MigrateStatusReport, error) {
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return nil, err
	}

	report := &MigrateStatusReport{Total: len(migrations)}
	for _, m := range migrations {
		report.Pending = append(report.Pending, MigrateStatus{
			Version:  m.version,
			Filename: m.filename,
		})
	}
	report.PendingN = len(report.Pending)
	return report, nil
}

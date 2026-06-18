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

		if err := applyMigration(db, m.version, m.filename, checksum, content); err != nil {
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
// and records its application in schema_migrations. The entire file content
// is passed to tx.Exec as a single multi-statement SQL string — go-sqlite3
// handles multi-statement execution natively.
func applyMigration(db queryable, version int, filename, checksum string, content []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Execute the entire migration file as a single multi-statement block.
	// go-sqlite3 supports this natively.
	if _, err := tx.Exec(string(content)); err != nil {
		return fmt.Errorf("%s: %w", filename, err)
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

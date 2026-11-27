package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/dbhealth"
)

// DoctorOptions configures the read-only SQLite doctor.
type DoctorOptions struct {
	Plane     string
	DBPath    string
	Quick     bool
	LedgerDir string
	Target    string
	Retention []RetentionCheck
	// AllowedTables, when non-nil, is the per-plane table allowlist. A table
	// outside it is reported (DB-03). Nil disables the check.
	AllowedTables map[string]bool
	// SkipIntegrity skips the page-level check. The boot warning-mode gate
	// uses it so startup is not delayed by a full file scan; the full check
	// stays in `admin db doctor` and nightly.
	SkipIntegrity bool
}

// RetentionCheck counts rows older than Cutoff. Cutoff is an RFC3339 string
// compared against the column (SQLite compares ISO-8601 text lexically).
type RetentionCheck struct {
	Table       string
	TimeColumn  string
	Cutoff      string
	Remediation string
}

// MigrationLedgerRow is one applied migration as recorded on disk.
type MigrationLedgerRow struct {
	Version  int
	Filename string
	Checksum string
}

// RunSQLiteDoctor performs the read-only content checks. It never deletes or
// vacuums; repairs stay with the ticket named in Remediation.
func RunSQLiteDoctor(ctx context.Context, db *sql.DB, opts DoctorOptions) []dbhealth.Finding {
	var findings []dbhealth.Finding
	add := func(check string, sev dbhealth.Severity, detail, remediation string) {
		findings = append(findings, dbhealth.Finding{
			Plane: opts.Plane, Check: check, Severity: sev, Detail: detail, Remediation: remediation,
		})
	}

	// Integrity: quick_check is the periodic gate; full integrity_check is a
	// nightly/window operation.
	pragma := "PRAGMA quick_check"
	if !opts.Quick {
		pragma = "PRAGMA integrity_check"
	}
	var status string
	if opts.SkipIntegrity {
		add("integrity", dbhealth.SeverityInfo, "skipped at boot (run `admin db doctor`)", "DB-08")
	} else if err := db.QueryRowContext(ctx, pragma).Scan(&status); err != nil {
		add("integrity", dbhealth.SeverityError, fmt.Sprintf("%s failed: %v", pragma, err), "DB-08: inspect the file, restore from backup")
	} else if status != "ok" {
		add("integrity", dbhealth.SeverityError, fmt.Sprintf("%s returned %q", pragma, status), "DB-08: restore from backup")
	} else {
		add("integrity", dbhealth.SeverityInfo, pragma+" ok", "")
	}

	if violations, err := ForeignKeyCheck(ctx, db); err != nil {
		add("foreign_key", dbhealth.SeverityError, err.Error(), "DB-01/DB-08: enable foreign_keys and repair children")
	} else if len(violations) > 0 {
		dbhealth.DBFKViolations.WithLabelValues(opts.Plane).Set(float64(len(violations)))
		sev := dbhealth.SeverityError
		for _, v := range violations {
			if strings.HasPrefix(v, "WARNING:") {
				sev = dbhealth.SeverityWarning
			}
		}
		add("foreign_key", sev, fmt.Sprintf("%d violation(s): %s", len(violations), strings.Join(violations, "; ")), "DB-01/DB-08: repair orphans before enabling writes")
	} else {
		dbhealth.DBFKViolations.WithLabelValues(opts.Plane).Set(0)
		add("foreign_key", dbhealth.SeverityInfo, "no violations", "")
	}

	if opts.LedgerDir != "" {
		findings = append(findings, ledgerFindings(ctx, db, opts.Plane, opts.LedgerDir)...)
	}

	for _, rc := range opts.Retention {
		if rc.Table == "" || rc.TimeColumn == "" || rc.Cutoff == "" {
			continue
		}
		var n int64
		q := fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" < ?`, rc.Table, rc.TimeColumn)
		if err := db.QueryRowContext(ctx, q, rc.Cutoff).Scan(&n); err != nil {
			add("retention", dbhealth.SeverityWarning, fmt.Sprintf("cannot measure %s.%s: %v", rc.Table, rc.TimeColumn, err), "DB-04/DB-05")
			continue
		}
		if n > 0 {
			add("retention", dbhealth.SeverityWarning, fmt.Sprintf("%s.%s has %d row(s) older than %s", rc.Table, rc.TimeColumn, n, rc.Cutoff), rc.Remediation)
		}
	}

	if opts.AllowedTables != nil {
		tables, err := AllUserTables(ctx, db)
		if err != nil {
			add("plane_allowlist", dbhealth.SeverityWarning, err.Error(), "DB-03")
		} else {
			var unexpected []string
			for _, table := range tables {
				if !opts.AllowedTables[table] {
					unexpected = append(unexpected, table)
				}
			}
			if len(unexpected) > 0 {
				add("plane_allowlist", dbhealth.SeverityWarning,
					fmt.Sprintf("%d table(s) outside the %s allowlist: %s", len(unexpected), opts.Plane, strings.Join(unexpected, ", ")),
					"DB-03: drop unreferenced business tables after backup; refresh the allowlist")
			}
		}
	}

	if opts.DBPath != "" {
		if size, err := DBSizeBytes(opts.DBPath); err == nil {
			wal, _ := WalSizeBytes(opts.DBPath)
			shm, _ := ShmSizeBytes(opts.DBPath)
			add("size", dbhealth.SeverityInfo, fmt.Sprintf("db=%dB wal=%dB shm=%dB", size, wal, shm), "")
		}
	}

	return findings
}

// ledgerFindings compares schema_migrations with the migration files on disk.
func ledgerFindings(ctx context.Context, db *sql.DB, plane, dir string) []dbhealth.Finding {
	// A consolidated baseline applies versions 1..N under version 0; those
	// individual files are intentionally absent from the ledger. Read the
	// baseline's declared coverage BEFORE opening the ledger cursor: the
	// DatabaseSet pool has one connection, so a second query while the cursor
	// is open would wait forever.
	baselineCovers := 0
	var baselineName string
	if err := db.QueryRowContext(ctx, `SELECT filename FROM schema_migrations WHERE version=0 AND filename LIKE '000_baseline_%' LIMIT 1`).Scan(&baselineName); err == nil && baselineName != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, baselineName)); err == nil {
			if m := baselineCoverRE.FindSubmatch(raw); len(m) == 2 {
				baselineCovers, _ = strconv.Atoi(string(m[1]))
			}
		}
	}

	rows, err := db.QueryContext(ctx,
		`SELECT version, filename, COALESCE(NULLIF(checksum_sha256, ''), checksum) FROM schema_migrations ORDER BY version`)
	if err != nil {
		return []dbhealth.Finding{{
			Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityError,
			Detail: fmt.Sprintf("read schema_migrations: %v", err), Remediation: "DB-08: run the migration runner",
		}}
	}
	defer func() { _ = rows.Close() }()

	byName := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []dbhealth.Finding{{
			Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityWarning,
			Detail: fmt.Sprintf("read migrations dir: %v", err), Remediation: "DB-08",
		}}
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		// Out-of-scope files are legitimately absent from this plane's ledger.
		if scope := parseMigrationScope(raw); scope != "all" && !scopeIncludes(scope, plane) {
			continue
		}
		byName[e.Name()] = digest.SHA256Bytes(raw)
	}

	var findings []dbhealth.Finding
	seen := map[string]bool{}
	for rows.Next() {
		var version int
		var filename, checksum string
		if err := rows.Scan(&version, &filename, &checksum); err != nil {
			findings = append(findings, dbhealth.Finding{Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityError, Detail: err.Error(), Remediation: "DB-08"})
			continue
		}
		seen[filename] = true
		disk, ok := byName[filename]
		if !ok {
			findings = append(findings, dbhealth.Finding{
				Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityError,
				Detail: fmt.Sprintf("ledger v%d references missing file %s", version, filename), Remediation: "DB-08: restore the migration file or repair the ledger",
			})
			continue
		}
		if checksum != "" && !strings.EqualFold(checksum, disk) {
			findings = append(findings, dbhealth.Finding{
				Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityError,
				Detail: fmt.Sprintf("%s checksum mismatch: ledger=%s disk=%s", filename, checksum, disk), Remediation: "DB-08: never edit applied migrations; add a new one",
			})
		}
	}
	if err := rows.Err(); err != nil {
		findings = append(findings, dbhealth.Finding{Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityError, Detail: err.Error(), Remediation: "DB-08"})
	}
	for name := range byName {
		if !seen[name] {
			// Baseline files are applied under a synthetic filename; do not
			// flag them as unapplied.
			if strings.HasPrefix(name, "000_baseline") {
				continue
			}
			// Files covered by the consolidated baseline are not in the
			// ledger individually by design.
			if v := migrationFileVersion(name); v > 0 && v <= baselineCovers {
				continue
			}
			findings = append(findings, dbhealth.Finding{
				Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityWarning,
				Detail: fmt.Sprintf("%s exists on disk but is not in schema_migrations", name), Remediation: "DB-08: run the migration runner",
			})
		}
	}
	if len(findings) == 0 {
		findings = append(findings, dbhealth.Finding{Plane: plane, Check: "migration_ledger", Severity: dbhealth.SeverityInfo, Detail: "ledger matches disk", Remediation: ""})
	}
	return findings
}

var baselineCoverRE = regexp.MustCompile(`baseline-covers:\s*(\d+)`)

func scopeIncludes(scope, plane string) bool {
	for _, tok := range strings.Split(scope, ",") {
		if strings.TrimSpace(tok) == plane {
			return true
		}
	}
	return false
}

func migrationFileVersion(name string) int {
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0
	}
	v, _ := strconv.Atoi(name[:idx])
	return v
}

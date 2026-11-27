package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

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

// GetMigrationStatusForPlane is the status an operator needs before applying:
// GetMigrationStatus restricted to the migrations that actually target
// targetDB. Out-of-scope files never gain a ledger row on this database, and the
// versions satisfied by an applied consolidated baseline sentinel are not
// pending work either — both are excluded instead of being reported forever as
// "pending".
//
// The execution-plane cutover exception (version 262 on primary) is deliberately
// not reproduced here: 262 is inside the baseline window, so the baseline check
// already covers it on every database that reached 265.
func GetMigrationStatusForPlane(db *sql.DB, targetDir, targetDB string) (*MigrateStatusReport, error) {
	if db == nil {
		return nil, fmt.Errorf("migration status: nil database")
	}
	migrations, err := discoverMigrations(targetDir)
	if err != nil {
		return nil, err
	}
	applied, err := loadAppliedMigrations(db)
	if err != nil {
		return nil, err
	}
	baselineThreshold := baselineThresholdForMigrations(migrations, targetDB)
	baselineCovered := baselineApplied(applied)
	selectedBaseline, hasSelectedBaseline := selectBaselineForPlane(migrations, targetDB)

	report := &MigrateStatusReport{}
	for _, m := range migrations {
		if !migrationAppliesToTargetDB(m.scope, targetDB) {
			continue
		}
		if isBaselineMigrationFile(m) {
			// Only the baseline this database actually applied — or the one it
			// would apply now — belongs in the report. The corpus also carries
			// the frozen shared dump and the other planes' dumps, and listing
			// them as applied would misstate what built this schema.
			if record, ok := applied[0]; ok {
				if record.filename != m.filename {
					continue
				}
			} else if !hasSelectedBaseline || selectedBaseline.filename != m.filename {
				continue
			}
		}
		ms := MigrateStatus{Version: m.version, Filename: m.filename}
		if content, err := os.ReadFile(m.path); err == nil {
			ms.Checksum = sha256Hex(content)
		}
		if rec, ok := applied[m.version]; ok {
			ms.Applied = true
			ms.Checksum = rec.checksum
			report.Applied = append(report.Applied, ms)
			continue
		}
		if baselineCovered && m.version > 0 && m.version <= baselineThreshold {
			continue
		}
		report.Pending = append(report.Pending, ms)
	}
	report.AppliedN = len(report.Applied)
	report.PendingN = len(report.Pending)
	report.Total = report.AppliedN + report.PendingN
	return report, nil
}

// FormatMigrateStatus formats a MigrateStatusReport as a human-readable table.
func FormatMigrateStatus(report *MigrateStatusReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-7s  %-40s %s\n", "version", "filename", "status")
	sb.WriteString(strings.Repeat("-", 70) + "\n")

	for _, m := range report.Applied {
		fmt.Fprintf(&sb, "%03d      %-40s applied\n", m.Version, m.Filename)
	}
	for _, m := range report.Pending {
		fmt.Fprintf(&sb, "%03d      %-40s pending\n", m.Version, m.Filename)
	}

	fmt.Fprintf(&sb, "\n%d migration(s) total: %d applied, %d pending\n",
		report.Total, report.AppliedN, report.PendingN)
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

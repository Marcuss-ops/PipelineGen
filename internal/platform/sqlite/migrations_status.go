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

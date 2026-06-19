package storage

import (
	"fmt"
	"strings"
)

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

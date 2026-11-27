package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// Canonical SQLite pragma set. Every opener in this package derives from this
// table: NewSQLiteDB, RunMigrationsOnDB and the DualPool. Keeping one
// definition prevents the divergence that silently disabled foreign-key
// enforcement on dbs.DualPool.Writer (audit DB-01, 2026-11-27).
const (
	canonicalJournalMode   = "WAL"
	canonicalSynchronous   = "NORMAL"
	canonicalBusyTimeoutMS = 5000
	canonicalForeignKeys   = "ON"
)

type sqlitePragma struct {
	name     string
	value    string
	uriValue string
}

var canonicalPragmas = []sqlitePragma{
	{name: "journal_mode", value: canonicalJournalMode, uriValue: "journal_mode(WAL)"},
	{name: "busy_timeout", value: fmt.Sprint(canonicalBusyTimeoutMS), uriValue: "busy_timeout(5000)"},
	{name: "synchronous", value: canonicalSynchronous, uriValue: "synchronous(NORMAL)"},
	{name: "foreign_keys", value: canonicalForeignKeys, uriValue: "foreign_keys(1)"},
}

// sqliteDSN builds the driver-level DSN for a file path. It is the only place
// that spells the query parameters; callers that need the eager write lock
// (the single-writer runtime pool) pass immediate=true.
func sqliteDSN(path string, immediate bool) string {
	dsn := fmt.Sprintf("%s?_journal_mode=%s&_foreign_keys=on&_busy_timeout=%d",
		path, canonicalJournalMode, canonicalBusyTimeoutMS)
	if immediate {
		dsn += "&_txlock=immediate"
	}
	return dsn
}

// pragmaURIParams returns the `_pragma=` values for the URI-based opener used
// by DualPool. They are derived from canonicalPragmas, so a new invariant is
// added in one place.
func pragmaURIParams() []string {
	values := make([]string, 0, len(canonicalPragmas))
	for _, p := range canonicalPragmas {
		values = append(values, p.uriValue)
	}
	return values
}

// applyCanonicalPragmas enforces the full set on an already-open handle. The
// journal_mode statement returns a row, so it is queried; the others are plain
// PRAGMA assignments.
func applyCanonicalPragmas(ctx context.Context, db *sql.DB) error {
	for _, p := range canonicalPragmas {
		statement := "PRAGMA " + p.name + " = " + p.value
		if p.name == "journal_mode" {
			var mode string
			if err := db.QueryRowContext(ctx, statement).Scan(&mode); err != nil {
				return fmt.Errorf("set %s=%s: %w", p.name, p.value, err)
			}
			continue
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("set %s=%s: %w", p.name, p.value, err)
		}
	}
	return nil
}

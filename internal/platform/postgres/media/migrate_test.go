package media_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// TestRunMediaMigrations_LedgerIsChecksumPinned proves D2 end to end against a
// live PostgreSQL instance. It creates a throwaway database because several
// media migrations guard on information_schema without a table_schema filter;
// a separate schema sharing public would make those guards observe the
// already-migrated media tables and skip their own ALTERs.
func TestRunMediaMigrations_LedgerIsChecksumPinned(t *testing.T) {
	dsn, _ := requirePostgresDSN(t)
	requireDestructiveTestDatabase(t)

	database := fmt.Sprintf("ledger_test_%d", time.Now().UnixNano())
	adminDSN := dsnWithDatabase(t, dsn, "postgres")
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	if err := admin.PingContext(context.Background()); err != nil {
		_ = admin.Close()
		t.Fatalf("ping admin database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE "`+database+`"`); err != nil {
		t.Fatalf("create throwaway database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP DATABASE IF EXISTS "`+database+`" WITH (FORCE)`)
	})

	db, err := sql.Open("pgx", dsnWithDatabase(t, dsn, database))
	if err != nil {
		t.Fatalf("open throwaway database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatalf("create vector extension: %v", err)
	}

	report, err := pgmedia.RunMediaMigrations(ctx, db)
	if err != nil {
		t.Fatalf("RunMediaMigrations first run: %v", err)
	}
	// 014 (ledger) + 10 ordered media migrations = 11.
	if len(report.Applied) != 11 {
		t.Fatalf("first run applied %d migrations, want 11: %v", len(report.Applied), report.Applied)
	}

	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&rows); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	if rows != 11 {
		t.Fatalf("ledger rows = %d, want 11", rows)
	}
	var filename, checksum string
	if err := db.QueryRowContext(ctx, `SELECT filename, checksum_sha256 FROM schema_migrations WHERE version = 1`).Scan(&filename, &checksum); err != nil {
		t.Fatalf("read version 1 ledger row: %v", err)
	}
	if filename != "001_media_schema.sql" || len(checksum) != 64 {
		t.Fatalf("version 1 ledger row = filename %q checksum %q, want 001_media_schema.sql and a SHA-256", filename, checksum)
	}

	second, err := pgmedia.RunMediaMigrations(ctx, db)
	if err != nil {
		t.Fatalf("RunMediaMigrations second run: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second run applied %d migrations, want 0: %v", len(second.Applied), second.Applied)
	}
	if len(second.Skipped) != 11 {
		t.Fatalf("second run skipped %d migrations, want 11", len(second.Skipped))
	}

	if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET checksum_sha256 = 'deadbeef' WHERE version = 1`); err != nil {
		t.Fatalf("tamper ledger checksum: %v", err)
	}
	if _, err := pgmedia.RunMediaMigrations(ctx, db); err == nil {
		t.Fatal("RunMediaMigrations accepted a tampered ledger checksum; want fail-closed error")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("RunMediaMigrations error = %v, want a checksum mismatch", err)
	}
}

// dsnWithDatabase replaces the database component of a URL-form PostgreSQL
// DSN. The live media suite is documented with URL DSNs; a keyword-form DSN is
// rejected so the test never guesses at a database it did not identify.
func dsnWithDatabase(t *testing.T, dsn, database string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_POSTGRES_DSN: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("TEST_POSTGRES_DSN must be a postgres:// URL-form DSN, got scheme %q", parsed.Scheme)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

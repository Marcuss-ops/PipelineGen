// Package media — testmain_test.go: DSN-gated PostgreSQL test fixture.
//
// The parity and integration tests in this package run against a live
// PostgreSQL 18 + pgvector instance (see docker-compose.test-postgres.yml).
// They are gated behind TEST_POSTGRES_DSN so the default `go test ./...`
// run stays hermetic: without the variable the tests SKIP (never fake
// availability — godlike/07).
//
// Usage:
//
//	make test-postgres
//	# or manually:
//	docker compose -f docker-compose.test-postgres.yml up -d --wait
//	TEST_POSTGRES_DSN=postgres://pipelinegen:pipelinegen@localhost:16432/pipelinegen_media_test?sslmode=disable \
//	  go test ./internal/platform/postgres/media/...
package media_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	pgmigration "github.com/Marcuss-ops/PipelineGen/migrations/postgres"
)

// requirePostgresDSN returns the DSN from TEST_POSTGRES_DSN and reports
// whether the live-database tests should run. It is a helper (deliberately
// not Test-prefixed) that marks the calling test as skipped when the
// variable is unset.
func requirePostgresDSN(t *testing.T) (string, bool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping live PostgreSQL tests (see docker-compose.test-postgres.yml)")
	}
	return dsn, true
}

// newMediaTestDB opens the live test database, applies the canonical
// media migrations (001 + 002), and truncates the media surfaces so each
// test starts from a clean transactional core. The embed bridge
// guarantees the DDL under test is byte-identical to the migration files.
func newMediaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, _ := requirePostgresDSN(t)
	requireDestructiveTestDatabase(t)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres (is the test container up? make test-postgres): %v", err)
	}

	if err := applyMediaMigrations(db); err != nil {
		t.Fatalf("apply media migrations: %v", err)
	}

	// Clean slate per test: truncate the transactional core in FK-safe
	// order. Derived surfaces (features/embeddings) cascade from assets.
	for _, stmt := range []string{
		`TRUNCATE asset_text_track_segments, asset_text_tracks`,
		`TRUNCATE asset_versions`,
		`TRUNCATE registry_events`,
		`TRUNCATE media_asset_sources`,
		`TRUNCATE outbox_events`,
		`TRUNCATE asset_renditions`,
		`TRUNCATE media_embedding_families`,
		`TRUNCATE asset_processing, asset_locations, media_asset_features, media_embeddings, media_assets CASCADE`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset %s: %v", firstToken(stmt), err)
		}
	}

	t.Cleanup(func() {
		for _, stmt := range []string{
			`TRUNCATE asset_text_tracks`,
			`TRUNCATE asset_versions`,
			`TRUNCATE registry_events`,
			`TRUNCATE media_asset_sources`,
			`TRUNCATE outbox_events`,
			`TRUNCATE asset_renditions`,
			`TRUNCATE media_embedding_families`,
			`TRUNCATE asset_processing, asset_locations, media_asset_features, media_embeddings, media_assets CASCADE`,
		} {
			_, _ = db.ExecContext(ctx, stmt)
		}
	})

	return db
}

// requireDestructiveTestDatabase fails closed unless TEST_POSTGRES_DSN points
// at a database that is unambiguously a TEST database.
//
// Why this exists: docker-compose.test-postgres.yml documents its container as
// "the durable local media database used by the native PipelineGen service",
// so that single server hosts the operational `pipelinegen_media` database
// next to `pipelinegen_media_test`. The truncating fixtures below had no
// protection at all against a DSN aimed at production, so one
//
//	TEST_POSTGRES_DSN=.../pipelinegen_media go test ./internal/platform/postgres/media/
//
// TRUNCATEd the live media_assets, asset_locations and asset_text_tracks —
// destroying committed assets and their Drive locations — while every
// assertion still passed. `make test-postgres` always used the right database,
// but a destructive fixture that trusts its input is a loaded gun.
//
// Read-only suites are unaffected: they call requirePostgresDSN directly and
// never reach this guard.
func requireDestructiveTestDatabase(t *testing.T) {
	t.Helper()
	name := databaseNameFromDSN(strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN")))
	if name == "" {
		t.Fatalf("TEST_POSTGRES_DSN has no resolvable database name; refusing to truncate an unidentified database (fail closed)")
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to truncate database %q: this fixture resets media_assets, asset_locations and asset_text_tracks, so it may only run against a *_test database (make test-postgres uses pipelinegen_media_test)", name)
	}
}

// databaseNameFromDSN extracts the database component from a libpq-style DSN in
// either the URL form (postgres://user:pw@host:port/dbname?params) or the
// keyword form (host=... dbname=...).
func databaseNameFromDSN(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return ""
		}
		return strings.Trim(parsed.Path, "/")
	}
	for _, field := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(field, "=")
		if ok && strings.EqualFold(key, "dbname") {
			return value
		}
	}
	return ""
}

func applyMediaMigrations(db *sql.DB) error {
	stmts := []string{
		pgmigration.MediaSchemaDDL,
		pgmigration.MediaVectorSurfacesDDL,
		pgmigration.MediaHNSWIndexesDDL,
		pgmigration.MediaTimestampsTimestamptzDDL,
		pgmigration.MediaAssetVersionsDDL,
		pgmigration.MediaClipFoldersDDL,
		pgmigration.MediaAssetProcessingDDL,
	}
	for i, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

func firstToken(s string) string {
	fields := strings.Fields(strings.TrimLeft(s, "TRUNCATE "))
	if len(fields) == 0 {
		return s
	}
	return strings.Join(fields, ",")
}

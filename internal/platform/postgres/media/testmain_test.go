// Package media — testmain_test.go: the package-local names of the shared
// DSN-gated PostgreSQL fixture.
//
// The fixture itself lives in internal/platform/postgres/media/pgtest so the
// one-off backfill tests (media/backfill) use the identical DSN gate,
// destructive-database guard and truncating reset. These wrappers keep the
// historical in-package helper names the 30+ live-runtime tests already call,
// so the fixture has one owner and the tests did not have to change.
//
// The tests run against a live PostgreSQL 18 + pgvector instance (see
// docker-compose.test-postgres.yml) and SKIP without TEST_POSTGRES_DSN.
package media_test

import (
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media/pgtest"
)

// requirePostgresDSN returns the DSN from TEST_POSTGRES_DSN and reports
// whether the live-database tests should run.
func requirePostgresDSN(t *testing.T) (string, bool) {
	t.Helper()
	return pgtest.RequireDSN(t)
}

// newMediaTestDB opens the live test database, applies the canonical media
// migrations, and truncates the media surfaces so each test starts clean.
func newMediaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return pgtest.NewTruncatedDB(t)
}

// requireDestructiveTestDatabase fails closed unless TEST_POSTGRES_DSN points
// at a database that is unambiguously a TEST database.
func requireDestructiveTestDatabase(t *testing.T) {
	t.Helper()
	pgtest.RequireDestructive(t)
}

// databaseNameFromDSN extracts the database component of a libpq-style DSN.
func databaseNameFromDSN(dsn string) string {
	return pgtest.DatabaseNameFromDSN(dsn)
}

// applyMediaMigrations applies the canonical ordered media migration sequence
// through its checksum-verified ledger.
func applyMediaMigrations(db *sql.DB) error {
	return pgtest.ApplyMigrations(db)
}

// openMediaDB opens the live test database with the canonical media DDL
// applied (no truncation), for tests that need a bespoke handle.
func openMediaDB(t *testing.T, dsn string) (*sql.DB, error) {
	t.Helper()
	return pgtest.OpenBespokeDB(t, dsn)
}

// touchMediaSource creates a real (empty) media file so a fixture's
// os.Stat liveness gate passes while injected fakes drive behaviour.
func touchMediaSource(t *testing.T) string {
	t.Helper()
	return pgtest.TouchMediaSource(t)
}

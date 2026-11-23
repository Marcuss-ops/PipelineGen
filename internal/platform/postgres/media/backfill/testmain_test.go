// Package backfill — testmain_test.go: the DSN-gated PostgreSQL fixture the
// one-off backfill/enrichment tests run against.
//
// The fixture itself is owned by internal/platform/postgres/media/pgtest, the
// same one the live-runtime tests use, so the DSN gate, the
// destructive-database guard and the truncating reset cannot drift between the
// two test packages. Only the bespoke (non-truncating) handle opener is local:
// it belongs to the tests that need a hand-applied DDL set.
package backfill_test

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

// requireDestructiveTestDatabase fails closed unless TEST_POSTGRES_DSN points
// at a database that is unambiguously a TEST database.
func requireDestructiveTestDatabase(t *testing.T) {
	t.Helper()
	pgtest.RequireDestructive(t)
}

// newMediaTestDB opens the live test database and resets the media surfaces.
func newMediaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return pgtest.NewTruncatedDB(t)
}

// openMediaDB opens the live test database with the canonical media DDL
// applied (no truncation), for tests that need a bespoke handle.
func openMediaDB(t *testing.T, dsn string) (*sql.DB, error) {
	t.Helper()
	return pgtest.OpenBespokeDB(t, dsn)
}

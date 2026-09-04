package media

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestRequireMediaPostgresDisabledDoesNotOpenDatabase(t *testing.T) {
	db, err := RequireMediaPostgres(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("disabled PostgreSQL should not fail: %v", err)
	}
	if db != nil {
		t.Fatal("disabled PostgreSQL must not open a database")
	}
}

func TestRequireMediaPostgresEnabledWithoutDSNFailsClosed(t *testing.T) {
	cfg := &config.Config{}
	cfg.MediaPostgreSQL.Enabled = true
	db, err := RequireMediaPostgres(context.Background(), cfg)
	if err == nil {
		t.Fatal("enabled PostgreSQL without DSN must fail closed")
	}
	if db != nil {
		t.Fatal("enabled PostgreSQL without DSN must not return a database")
	}
}

// A configured DSN that cannot be reached must fail closed: a half-open media
// SSOT must never boot.
func TestRequireMediaPostgresFailsClosedOnUnreachableBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("requires network stack")
	}
	cfg := &config.Config{}
	cfg.MediaPostgreSQL.Enabled = true
	cfg.MediaPostgreSQL.DSN = "postgres://pipelogen:pipelogen@127.0.0.1:1/no_such_media_db?sslmode=disable"
	if _, err := RequireMediaPostgres(context.Background(), cfg); err == nil {
		t.Fatal("enabled PostgreSQL with unreachable backend must fail closed")
	}
}

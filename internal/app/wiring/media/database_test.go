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

func TestRequireMediaPostgresEnabledFailsClosedWithoutDSN(t *testing.T) {
	cfg := &config.Config{}
	cfg.MediaPostgreSQL.Enabled = true
	if _, err := RequireMediaPostgres(context.Background(), cfg); err == nil {
		t.Fatal("enabled PostgreSQL without DSN must fail closed")
	}
}

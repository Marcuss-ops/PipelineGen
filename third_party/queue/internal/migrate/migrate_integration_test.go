package migrate

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Marcuss-ops/RenderingGen/queue/migrations"
)

// TestApplyAgainstPostgres applies the migrations to a real database and
// verifies idempotency and the resulting schema. Set TEST_DATABASE_URL to run
// it (skipped otherwise, e.g. in unit CI without a service container).
func TestApplyAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping postgres integration test")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Idempotency: a second apply must be a no-op.
	if err := Apply(ctx, db); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	for _, table := range []string{
		"render_jobs",
		"render_attempts",
		"render_artifacts",
		"rendering_workers",
		"render_events",
		"processing_metrics",
		"render_telemetry",
		"worker_heartbeats",
	} {
		var exists bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	// Derive the expected count from the embedded migration list instead of
	// hardcoding it: a new migration file must not require editing this test
	// (the schema itself is what the assertions above pin).
	names, err := migrations.Names()
	if err != nil {
		t.Fatal(err)
	}
	if count != len(names) {
		t.Errorf("want %d applied migrations, got %d", len(names), count)
	}
}

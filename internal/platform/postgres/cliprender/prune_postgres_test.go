package cliprender_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pgcliprender "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/cliprender"
)

// TestRenderCachePrunerSweepsOneThousandEntries pins the bounded-cache
// behavior with 1,000 real cache rows in the same clip_render_cache table the
// worker writes. This is the DB-level half of CR-11: the pruner must count
// them in dry-run and delete every old row on apply while leaving a fresh row
// alone.
//
// The rows are inserted directly (not by running 1,000 GPU renders) because
// the property under test is retention of the cache table, not render output.
// The test is gated by TEST_POSTGRES_DSN like the rest of the live media
// PostgreSQL suite.
func TestRenderCachePrunerSweepsOneThousandEntries(t *testing.T) {
	db := requireRenderCachePostgres(t)
	ctx := context.Background()
	const marker = "cr11-test/"
	cleanup := func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM clip_render_cache WHERE storage_key LIKE $1`, marker+"%")
	}
	cleanup()
	t.Cleanup(cleanup)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	const oldCreated = "2000-01-01T00:00:00Z"
	const freshCreated = "2026-11-22T00:00:00Z"
	sha := strings.Repeat("a", 64)
	for i := 0; i < 1000; i++ {
		fp := fmt.Sprintf("%064x", i)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO clip_render_cache
				(fingerprint, asset_id, storage_key, artifact_url, content_type, sha256, size_bytes, created_at)
			VALUES ($1, $2, $3, $4, 'video/mp4', $5, 1, $6)`,
			fp, fmt.Sprintf("cr11-test-asset-%d", i), fmt.Sprintf("%s%d", marker, i),
			"https://example.test/cr11.mp4", sha, oldCreated); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO clip_render_cache
			(fingerprint, asset_id, storage_key, artifact_url, content_type, sha256, size_bytes, created_at)
		VALUES ($1, $2, $3, $4, 'video/mp4', $5, 1, $6)`,
		fmt.Sprintf("%064x", 1000), "cr11-test-asset-fresh", marker+"fresh",
		"https://example.test/cr11-fresh.mp4", sha, freshCreated); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert fresh row: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	count := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clip_render_cache WHERE `+where, args...).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", where, err)
		}
		return n
	}
	if got := count("storage_key LIKE $1", marker+"%"); got != 1001 {
		t.Fatalf("seed rows = %d, want 1001", got)
	}

	cutoff := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	pruner := pgcliprender.NewRenderCachePruner(db)
	rows, err := pruner.PruneOlderThan(ctx, cutoff, true)
	if err != nil {
		t.Fatalf("dry-run prune: %v", err)
	}
	if rows < 1000 {
		t.Fatalf("dry-run rows = %d, want at least the 1000 seeded rows", rows)
	}
	if got := count("storage_key LIKE $1 AND created_at = $2", marker+"%", oldCreated); got != 1000 {
		t.Fatalf("dry-run deleted rows: old=%d, want 1000", got)
	}

	rows, err = pruner.PruneOlderThan(ctx, cutoff, false)
	if err != nil {
		t.Fatalf("apply prune: %v", err)
	}
	if rows < 1000 {
		t.Fatalf("apply rows = %d, want at least 1000", rows)
	}
	if got := count("storage_key LIKE $1 AND created_at = $2", marker+"%", oldCreated); got != 0 {
		t.Fatalf("old rows after apply = %d, want 0", got)
	}
	if got := count("storage_key = $1", marker+"fresh"); got != 1 {
		t.Fatalf("fresh row after apply = %d, want 1", got)
	}
}

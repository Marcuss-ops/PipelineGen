package performance

import (
	"context"
	"testing"
	"time"
)

// seedWorkHistory inserts rows directly (created_at is controlled so the
// newest-first ordering is pinned deterministically).
func seedWorkHistory(t *testing.T, store *OperationStore, rows [][]any) {
	t.Helper()
	for _, row := range rows {
		if _, err := store.db.ExecContext(context.Background(), `INSERT INTO performance_operations
			(operation_id, run_id, job_id, step_id, operation, elapsed_ms, source_duration_ms,
			 source_size_bytes, output_size_bytes, width, height, fps, cache_hit, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			row...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListWorkHistoryReturnsNewestFirst(t *testing.T) {
	store := newOperationsStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	seedWorkHistory(t, store, [][]any{
		{"op-old", "run-1", "job-1", "", "chronon.render_loop", 1000, 10000, 0, 0, 0, 0, 0, 0, now},
		{"op-new", "run-1", "job-1", "", "chronon.render_loop", 2000, 20000, 0, 0, 0, 0, 0, 0, now},
		{"op-zero", "run-1", "job-1", "", "chronon.startup", 0, 0, 0, 0, 0, 0, 0, 0, now},
	})

	got, err := store.ListWorkHistory(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d rows, want 2 (zero-elapsed row must be excluded)", len(got))
	}
	if got[0].Operation != "chronon.render_loop" || got[0].ElapsedMS != 2000 {
		t.Fatalf("got[0] = %+v, want newest chronon.render_loop elapsed 2000", got[0])
	}
	if got[1].ElapsedMS != 1000 {
		t.Fatalf("got[1] = %+v, want oldest elapsed 1000", got[1])
	}
}

func TestListWorkHistoryProjectsMeasuredFacts(t *testing.T) {
	store := newOperationsStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	seedWorkHistory(t, store, [][]any{
		{"op-1", "run-1", "job-1", "", "probe", 3500, 120000, 64_000_000, 1_200_000, 1920, 1080, 30.0, 1, now},
	})

	got, err := store.ListWorkHistory(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d rows, want 1", len(got))
	}
	row := got[0]
	if row.Operation != "probe" || row.ElapsedMS != 3500 ||
		row.SourceDurationMS != 120000 || row.SourceSizeBytes != 64_000_000 ||
		row.OutputSizeBytes != 1_200_000 || row.Width != 1920 || row.Height != 1080 ||
		row.FPS != 30.0 || !row.CacheHit {
		t.Fatalf("projected row = %+v, want all measured facts verbatim", row)
	}
}

func TestListWorkHistoryHonorsLimit(t *testing.T) {
	store := newOperationsStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows := make([][]any, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, []any{
			"op-" + string(rune('a'+i)), "run-1", "job-1", "", "probe", int64(1000 + i), 0, 0, 0, 0, 0, 0, 0, now,
		})
	}
	seedWorkHistory(t, store, rows)

	got, err := store.ListWorkHistory(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("returned %d rows, want 3 (limit)", len(got))
	}
	// Newest first.
	if got[0].ElapsedMS != 1004 {
		t.Fatalf("got[0].ElapsedMS = %d, want 1004 (newest first)", got[0].ElapsedMS)
	}
}

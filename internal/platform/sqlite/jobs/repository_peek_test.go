package jobs

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

func TestPeekQueued_ReturnsOrderedLimitedQueuedJobsReadOnly(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()
	now := time.Now().UTC()

	insert := func(id string, status job.Status, priority int, createdAt time.Time) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
				correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
				worker_id, lease_id, lease_expiry, created_at, updated_at, revision)
			VALUES (?, ?, ?, ?, '', '', '', '', '{}', '{}', 0, '', 0, 3, '', '', NULL, ?, ?, 1)`,
			id, "test.job", status, priority,
			timeutil.FormatRFC3339(createdAt), timeutil.FormatRFC3339(createdAt))
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	insert("queued-low", job.StatusQueued, 1, now.Add(-3*time.Minute))
	insert("queued-high", job.StatusQueued, 10, now.Add(-2*time.Minute))
	insert("running-high", job.StatusRunning, 100, now.Add(-time.Minute))
	insert("queued-mid", job.StatusQueued, 5, now)

	before := make(map[string]job.Job)
	for _, id := range []string{"queued-low", "queued-high", "running-high", "queued-mid"} {
		got, err := store.Get(ctx, id)
		if err != nil || got == nil {
			t.Fatalf("Get %s: got=%v err=%v", id, got, err)
		}
		before[id] = *got
	}

	got, err := store.PeekQueued(ctx, 2)
	if err != nil {
		t.Fatalf("PeekQueued: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("PeekQueued returned %d jobs, want 2", len(got))
	}
	if got[0].ID != "queued-high" || got[1].ID != "queued-mid" {
		t.Fatalf("PeekQueued ordering = [%s, %s], want [queued-high, queued-mid]", got[0].ID, got[1].ID)
	}

	for id, expected := range before {
		actual, err := store.Get(ctx, id)
		if err != nil || actual == nil {
			t.Fatalf("Get after PeekQueued %s: got=%v err=%v", id, actual, err)
		}
		if actual.Status != expected.Status || actual.Revision != expected.Revision ||
			actual.WorkerID != expected.WorkerID || actual.LeaseID != expected.LeaseID {
			t.Errorf("PeekQueued mutated %s: before status=%s revision=%d worker=%q lease=%q; after status=%s revision=%d worker=%q lease=%q",
				id, expected.Status, expected.Revision, expected.WorkerID, expected.LeaseID,
				actual.Status, actual.Revision, actual.WorkerID, actual.LeaseID)
		}
	}
}

func TestPeekQueued_NonPositiveLimitReturnsEmptyWithoutMutation(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	got, err := store.PeekQueued(context.Background(), 0)
	if err != nil {
		t.Fatalf("PeekQueued(0): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("PeekQueued(0) = %#v, want non-nil empty slice", got)
	}
}

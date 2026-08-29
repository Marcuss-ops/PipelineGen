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

// TestFutureJobReader_PeekQueuedIsReadOnlyDrivesThroughPort exercises the
// canonical FutureJobReader port (not the concrete method): a queued job
// peeked via the port must be left byte-identical on every field the claim
// path owns — status, revision, retry_count, worker/lease fields — proving
// the lookahead never races ClaimNext.
func TestFutureJobReader_PeekQueuedIsReadOnlyDrivesThroughPort(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	var reader job.FutureJobReader = store

	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO jobs (id, type, status, priority, correlation_id, payload_json, result_json,
			progress, error, retry_count, max_retries, worker_id, lease_id, lease_expiry,
			created_at, updated_at, revision)
		VALUES (?, 'script.generate', ?, 7, 'corr', '{}', '{}', 0, '', 1, 3, 'worker-9', 'lease-9', ?, ?, ?, 4)`,
		"future-job", job.StatusQueued,
		timeutil.FormatRFC3339(now.Add(time.Minute)), // lease_expiry set (like a previously claimed job)
		timeutil.FormatRFC3339(now.Add(-time.Minute)),
		timeutil.FormatRFC3339(now)); err != nil {
		t.Fatalf("insert queued job: %v", err)
	}

	peeked, err := reader.PeekQueued(ctx, 1)
	if err != nil {
		t.Fatalf("PeekQueued via port: %v", err)
	}
	if len(peeked) != 1 || peeked[0].ID != "future-job" {
		t.Fatalf("PeekQueued = %#v, want exactly the queued job", peeked)
	}

	after, err := store.Get(ctx, "future-job")
	if err != nil || after == nil {
		t.Fatalf("Get after peek: got=%v err=%v", after, err)
	}
	// Contract: none of the claim/lease-owned fields may change.
	if after.Status != job.StatusQueued {
		t.Errorf("status mutated: %q", after.Status)
	}
	if after.Revision != 4 {
		t.Errorf("revision mutated: %d", after.Revision)
	}
	if after.RetryCount != 1 {
		t.Errorf("retry_count mutated: %d", after.RetryCount)
	}
	if after.WorkerID != "worker-9" || after.LeaseID != "lease-9" {
		t.Errorf("worker/lease mutated: worker=%q lease=%q", after.WorkerID, after.LeaseID)
	}
	// FormatRFC3339 truncates to seconds, so compare against the stored precision.
	wantLeaseExpiry := now.Add(time.Minute).Truncate(time.Second)
	if after.LeaseExpiry == nil || !after.LeaseExpiry.Equal(wantLeaseExpiry) {
		t.Errorf("lease_expiry mutated: %v, want %v", after.LeaseExpiry, wantLeaseExpiry)
	}
}

// TestFutureJobReader_PriorityOrderMatchesClaimNextOrder ensures the port
// exposes jobs in the same order ClaimNext will claim them
// (priority DESC, created_at ASC) so preparation work targets the actual
// next jobs rather than an arbitrary queue snapshot.
func TestFutureJobReader_PriorityOrderMatchesClaimNextOrder(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	var reader job.FutureJobReader = store
	now := time.Now().UTC()
	insert := func(id string, priority int, createdAt time.Time) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `
			INSERT INTO jobs (id, type, status, priority, correlation_id, payload_json, result_json,
				progress, error, retry_count, max_retries, worker_id, lease_id, lease_expiry,
				created_at, updated_at, revision)
			VALUES (?, 'clip.render', ?, ?, '', '{}', '{}', 0, '', 0, 3, '', '', NULL, ?, ?, 1)`,
			id, job.StatusQueued, priority,
			timeutil.FormatRFC3339(createdAt), timeutil.FormatRFC3339(createdAt)); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("older-low", 1, now.Add(-3*time.Minute))
	insert("newer-low", 1, now)
	insert("high", 99, now.Add(-time.Minute))

	got, err := reader.PeekQueued(ctx, 10)
	if err != nil {
		t.Fatalf("PeekQueued: %v", err)
	}
	want := []string{"high", "older-low", "newer-low"}
	if len(got) != len(want) {
		t.Fatalf("PeekQueued len = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func cancelArtifact() model.Artifact {
	return model.Artifact{StorageKey: "k", ArtifactHash: "h", SizeBytes: 1, ContentType: "video/mp4"}
}

// assertAttemptStatus checks the append-only render_attempts ledger row.
func assertAttemptStatus(t *testing.T, db *sql.DB, jobID, want string) {
	t.Helper()
	var status string
	err := db.QueryRow(`SELECT status FROM render_attempts WHERE job_id = $1 ORDER BY attempt_number DESC LIMIT 1`, jobID).Scan(&status)
	if err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("attempt status = %q, want %q", status, want)
	}
}

func TestCancelRunningJobFreezesAttemptsAcrossLeaseCycle(t *testing.T) {
	r, db := setupRepo(t, 10*time.Millisecond, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "pg-cancel-running", RenderPlan: []byte(`{"o":1}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := r.Claim(context.Background(), "worker-1")
	if err != nil || claimed == nil {
		t.Fatalf("claim = %v/%v, want job", claimed, err)
	}
	if err := r.Cancel(context.Background(), "pg-cancel-running"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	job, err := r.Get(context.Background(), "pg-cancel-running")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateCancelled || job.Attempts != 1 {
		t.Fatalf("after cancel: state=%q attempts=%d, want cancelled/1", job.State, job.Attempts)
	}
	assertAttemptStatus(t, db, "pg-cancel-running", "cancelled")

	// The in-flight worker's reports must be rejected: the job is no longer
	// running, so neither complete nor fail can move/requeue it.
	if err := r.Complete(context.Background(), "pg-cancel-running", "worker-1", cancelArtifact()); err == nil {
		t.Fatal("complete on cancelled job must be rejected")
	}
	if err := r.Fail(context.Background(), "pg-cancel-running", "worker-1", "late failure"); err == nil {
		t.Fatal("fail on cancelled job must be rejected")
	}

	// Full lease/claim cycle: lease elapses, the expiry scan runs, and claims
	// are attempted repeatedly. The job must never come back.
	for cycle := 0; cycle < 3; cycle++ {
		time.Sleep(15 * time.Millisecond)
		if n, err := r.RequeueExpired(context.Background(), time.Now()); err != nil || n != 0 {
			t.Fatalf("cycle %d: RequeueExpired = %d/%v, want 0", cycle, n, err)
		}
		if again, _, err := r.Claim(context.Background(), "worker-2"); err != nil || again != nil {
			t.Fatalf("cycle %d: claim = %v/%v, want nil", cycle, again, err)
		}
		job, _ := r.Get(context.Background(), "pg-cancel-running")
		if job.State != model.StateCancelled || job.Attempts != 1 {
			t.Fatalf("cycle %d: state=%q attempts=%d, want cancelled/1", cycle, job.State, job.Attempts)
		}
	}
	// Only one attempt row exists: the in-flight one, closed as cancelled.
	assertAttemptStatus(t, db, "pg-cancel-running", "cancelled")
	var attemptCount int
	if err := db.QueryRow(`SELECT count(*) FROM render_attempts WHERE job_id = $1`, "pg-cancel-running").Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 1 {
		t.Fatalf("attempt rows = %d, want 1 (one Chronon invocation, never re-run)", attemptCount)
	}

	// Event ledger: JOB_CREATED, JOB_CLAIMED, JOB_CANCELLED — and no requeue,
	// complete or second-claim event.
	var events []string
	rows, err := db.Query(`SELECT event_type FROM render_events WHERE job_id = $1 ORDER BY id`, "pg-cancel-running")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	wantEvents := []string{"JOB_CREATED", "JOB_CLAIMED", "JOB_CANCELLED"}
	if len(events) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Fatalf("events = %v, want %v", events, wantEvents)
		}
	}
}

func TestCancelPendingJobIsNeverClaimable(t *testing.T) {
	r, db := setupRepo(t, time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "pg-cancel-pending", RenderPlan: []byte(`{"o":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(context.Background(), "pg-cancel-pending"); err != nil {
		t.Fatal(err)
	}
	if job, err := r.Get(context.Background(), "pg-cancel-pending"); err != nil || job.State != model.StateCancelled || job.Attempts != 0 {
		t.Fatalf("after cancel: %v state=%v attempts=%d, want cancelled/0", err, job.State, job.Attempts)
	}
	if claimed, _, err := r.Claim(context.Background(), "worker-1"); err != nil || claimed != nil {
		t.Fatalf("claim = %v/%v, want nil", claimed, err)
	}
	// A job cancelled before any claim has zero attempt rows: Chronon was
	// never invoked, so the append-only ledger must stay empty.
	var attemptCount int
	if err := db.QueryRow(`SELECT count(*) FROM render_attempts WHERE job_id = $1`, "pg-cancel-pending").Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt rows = %d, want 0 (Chronon never invoked)", attemptCount)
	}
}

func TestCancelIdempotentAndTerminalConflict(t *testing.T) {
	r, _ := setupRepo(t, time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "pg-cancel-2x", RenderPlan: []byte(`{"o":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(context.Background(), "pg-cancel-2x"); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(context.Background(), "pg-cancel-2x"); err != nil {
		t.Fatalf("second cancel must be a no-op: %v", err)
	}

	if err := r.Submit(context.Background(), model.Job{ID: "pg-completed", RenderPlan: []byte(`{"o":1}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), "pg-completed", "worker-1", cancelArtifact()); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(context.Background(), "pg-completed"); err == nil {
		t.Fatal("cancel completed must fail")
	}
}

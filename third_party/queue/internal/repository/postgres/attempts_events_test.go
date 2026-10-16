package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// attemptRow is the subset of render_attempts the tests assert on.
type attemptRow struct {
	Number int
	Status string
	Worker string
	Error  string
}

// queryAttempts returns the attempts of a job ordered by attempt number.
func queryAttempts(t *testing.T, db *sql.DB, jobID string) []attemptRow {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT attempt_number, status, COALESCE(worker_id, ''), COALESCE(error_message, '')
		FROM render_attempts
		WHERE job_id = $1
		ORDER BY attempt_number`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := rows.Scan(&a.Number, &a.Status, &a.Worker, &a.Error); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// queryEvents returns the event types of a job in insertion order.
func queryEvents(t *testing.T, db *sql.DB, jobID string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT event_type
		FROM render_events
		WHERE job_id = $1
		ORDER BY id`, jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasEvent(events []string, want string) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

func TestAttemptsAndEventsOnClaimComplete(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	attempts := queryAttempts(t, db, "job-1")
	if len(attempts) != 1 {
		t.Fatalf("want 1 attempt, got %d", len(attempts))
	}
	if attempts[0].Number != 1 || attempts[0].Status != "running" || attempts[0].Worker != "w1" {
		t.Fatalf("unexpected attempt: %+v", attempts[0])
	}

	if err := r.Complete(context.Background(), "job-1", "w1", model.Artifact{}); err != nil {
		t.Fatal(err)
	}

	attempts = queryAttempts(t, db, "job-1")
	if len(attempts) != 1 || attempts[0].Status != "completed" {
		t.Fatalf("attempt not completed: %+v", attempts)
	}

	events := queryEvents(t, db, "job-1")
	for _, want := range []string{"JOB_CREATED", "JOB_CLAIMED", "JOB_COMPLETED"} {
		if !hasEvent(events, want) {
			t.Fatalf("missing event %s in %v", want, events)
		}
	}
}

func TestAttemptsPreservedAcrossFailures(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}

	// Attempt 1 fails -> requeue.
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Fail(context.Background(), "job-1", "w1", "boom"); err != nil {
		t.Fatal(err)
	}

	// Attempt 2 claims and completes.
	if _, _, err := r.Claim(context.Background(), "w2"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), "job-1", "w2", model.Artifact{}); err != nil {
		t.Fatal(err)
	}

	attempts := queryAttempts(t, db, "job-1")
	if len(attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(attempts))
	}
	if attempts[0].Status != "failed" || attempts[0].Error != "boom" {
		t.Fatalf("attempt 1 should be failed with error, got %+v", attempts[0])
	}
	if attempts[1].Status != "completed" || attempts[1].Worker != "w2" {
		t.Fatalf("attempt 2 should be completed by w2, got %+v", attempts[1])
	}

	events := queryEvents(t, db, "job-1")
	if !hasEvent(events, "JOB_REQUEUED") {
		t.Fatalf("missing JOB_REQUEUED in %v", events)
	}
}

func TestLeaseExpiryMarksAttempt(t *testing.T) {
	r, db := setupRepo(t, 10*time.Millisecond, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Millisecond)
	if _, err := r.RequeueExpired(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	attempts := queryAttempts(t, db, "job-1")
	if len(attempts) != 1 || attempts[0].Status != "lease_expired" {
		t.Fatalf("attempt should be lease_expired, got %+v", attempts)
	}
}

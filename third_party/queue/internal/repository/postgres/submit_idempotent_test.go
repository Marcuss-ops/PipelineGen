package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsSubmitConflictClassification pins which failures SubmitIdempotent is
// allowed to reinterpret as "another submitter won the key race".
func TestIsSubmitConflictClassification(t *testing.T) {
	if !isSubmitConflict(fmt.Errorf("insert: %w", repository.ErrJobExists)) {
		t.Fatal("ErrJobExists must be retryable as a duplicate-id conflict")
	}
	if !isSubmitConflict(&pgconn.PgError{Code: "23505", ConstraintName: "render_jobs_idempotency_key_unique"}) {
		t.Fatal("a unique violation on the idempotency index must be retryable")
	}
	if isSubmitConflict(&pgconn.PgError{Code: "23505", ConstraintName: "render_jobs_pkey"}) {
		t.Fatal("a primary-key violation that is not a job-id conflict must not be masked")
	}
	if isSubmitConflict(errors.New("unknown job_type \"bogus\"")) {
		t.Fatal("a validation error must never be treated as a submit race")
	}
	if isSubmitConflict(&pgconn.PgError{Code: "23503", ConstraintName: "render_jobs_idempotency_key_unique"}) {
		t.Fatal("a foreign-key violation must not be treated as a uniqueness race")
	}
}

// TestSubmitIdempotentSurfacesValidationError pins the caller-visible half:
// an insert refused for a reason other than uniqueness must return that reason,
// not a lookup error (or a canonical row that happens to share the key).
func TestSubmitIdempotentSurfacesValidationError(t *testing.T) {
	r := newRepo(t, time.Minute, 3)
	ctx := context.Background()
	job := model.Job{
		ID:             "bad-job-type",
		JobType:        "definitely-not-a-job-type",
		IdempotencyKey: "bad-job-type-key",
		RenderPlan:     []byte(`{}`),
	}
	_, _, err := r.SubmitIdempotent(ctx, job)
	if err == nil || !strings.Contains(err.Error(), "unknown job_type") {
		t.Fatalf("SubmitIdempotent error = %v, want the original unknown job_type error", err)
	}
	if _, getErr := r.Get(ctx, job.ID); !errors.Is(getErr, repository.ErrNotFound) {
		t.Fatalf("invalid job was persisted: Get error = %v", getErr)
	}
}

func TestSubmitIdempotentRejectsChangedRequestUnderSameKey(t *testing.T) {
	r := newRepo(t, time.Minute, 3)
	ctx := context.Background()
	first := model.Job{ID: "job-identity", IdempotencyKey: "request-key", RenderPlan: []byte(`{"n":1}`)}
	if _, created, err := r.SubmitIdempotent(ctx, first); err != nil || !created {
		t.Fatalf("initial submit: created=%t err=%v", created, err)
	}
	changed := first
	changed.RenderPlan = []byte(`{"n":2}`)
	if _, created, err := r.SubmitIdempotent(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) || created {
		t.Fatalf("changed replay: created=%t err=%v, want idempotency conflict", created, err)
	}
	canonical, err := r.Get(ctx, first.ID)
	if err != nil || canonical == nil || !model.SameSubmitRequest(*canonical, first) {
		t.Fatalf("canonical request changed after conflicting replay: job=%+v err=%v", canonical, err)
	}
}

package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsSubmitConflictRecognizesBothIdempotencyIndexes pins the real schema
// detail: migration 007 created idx_render_jobs_idempotency and migration 011
// added render_jobs_idempotency_key_unique. PostgreSQL reports whichever index
// it evaluated first, so classifying only the 011 spelling would make the
// SubmitIdempotent race path driver-error-dependent on the deployed schema.
func TestIsSubmitConflictRecognizesBothIdempotencyIndexes(t *testing.T) {
	for _, name := range []string{
		"idx_render_jobs_idempotency",
		"render_jobs_idempotency_key_unique",
	} {
		if !isSubmitConflict(&pgconn.PgError{Code: "23505", ConstraintName: name}) {
			t.Fatalf("%s: unique violation was not classified as a submit conflict", name)
		}
	}
}

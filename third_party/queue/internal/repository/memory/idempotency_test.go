package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// TestSubmitIdempotentReturnsStoredPendingJob pins the canonical projection of
// a freshly created job: the state and timestamps are stamped on the STORED
// clone, so the caller must receive that row (not the untouched input struct).
func TestSubmitIdempotentReturnsStoredPendingJob(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)
	got, created, err := repo.SubmitIdempotent(ctx, model.Job{
		ID: "job-1", IdempotencyKey: "fresh-key", RenderPlan: []byte(`{"n":1}`),
	})
	if err != nil || !created {
		t.Fatalf("submit idempotent: created=%t err=%v", created, err)
	}
	if got == nil || got.State != model.StatePending {
		t.Fatalf("created job = %+v, want the stored pending row", got)
	}
	if got.CreatedAt.IsZero() || got.QueuedAt.IsZero() {
		t.Fatalf("created job misses its stamped timestamps: created=%v queued=%v", got.CreatedAt, got.QueuedAt)
	}
}

func TestSubmitIdempotentRejectsChangedRequestUnderSameKey(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)
	first := model.Job{ID: "job-identity", IdempotencyKey: "request-key", RenderPlan: []byte(`{"n":1}`)}
	if _, created, err := repo.SubmitIdempotent(ctx, first); err != nil || !created {
		t.Fatalf("initial submit: created=%t err=%v", created, err)
	}
	changed := first
	changed.RenderPlan = []byte(`{"n":2}`)
	if _, created, err := repo.SubmitIdempotent(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) || created {
		t.Fatalf("changed replay: created=%t err=%v, want idempotency conflict", created, err)
	}
	canonical, err := repo.Get(ctx, first.ID)
	if err != nil || string(canonical.RenderPlan) != `{"n":1}` {
		t.Fatalf("canonical job changed after conflicting replay: job=%+v err=%v", canonical, err)
	}
}

// TestSubmitRejectsReusedIdempotencyKey pins the in-memory mirror of the
// PostgreSQL unique index: a non-empty key identifies ONE logical job on EVERY
// insert path. Submit used to skip the idem index entirely, so the same key was
// accepted here and rejected by PostgreSQL.
func TestSubmitRejectsReusedIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)

	if err := repo.Submit(ctx, model.Job{ID: "job-first", IdempotencyKey: "run-1/chunk-0", RenderPlan: []byte(`{"n":1}`)}); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	err := repo.Submit(ctx, model.Job{ID: "job-second", IdempotencyKey: "run-1/chunk-0", RenderPlan: []byte(`{"n":2}`)})
	if !errors.Is(err, repository.ErrJobExists) {
		t.Fatalf("reused key: want ErrJobExists, got %v", err)
	}
	if _, err := repo.Get(ctx, "job-second"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("conflicting submit must not be stored, got %v", err)
	}
	if got, err := repo.Get(ctx, "job-first"); err != nil || got.State != model.StatePending {
		t.Fatalf("canonical job = %+v, %v; want the untouched first job", got, err)
	}
}

// TestSubmitBatchRejectsReusedKeyAtomically pins the batch half: a key already
// present in the store rejects the WHOLE family before the first insert, so an
// anchor is never stored without its children (the partial-family state the
// batch API exists to prevent).
func TestSubmitBatchRejectsReusedKeyAtomically(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)

	if err := repo.Submit(ctx, model.Job{ID: "seed", IdempotencyKey: "run-2/chunk-0", RenderPlan: []byte(`{"n":1}`)}); err != nil {
		t.Fatalf("seed submit: %v", err)
	}
	err := repo.SubmitBatch(ctx, []model.Job{
		{ID: "anchor", RenderPlan: []byte(`{"n":2}`)},
		{ID: "anchor-chunk-0", ParentJobID: "anchor", ChunkIndex: 0,
			FrameRange:     &model.FrameRange{Start: 0, End: 120},
			IdempotencyKey: "run-2/chunk-0", RenderPlan: []byte(`{"n":3}`)},
	})
	if !errors.Is(err, repository.ErrJobExists) {
		t.Fatalf("batch reusing a stored key: want ErrJobExists, got %v", err)
	}
	for _, id := range []string{"anchor", "anchor-chunk-0"} {
		if _, getErr := repo.Get(ctx, id); !errors.Is(getErr, repository.ErrNotFound) {
			t.Fatalf("batch left %s behind after rejection: %v", id, getErr)
		}
	}
}

// TestSubmitBatchRejectsDuplicateKeyInsideBatch pins the shared validation that
// both backends run before mutating anything: two jobs in one family may not
// claim the same logical identity.
func TestSubmitBatchRejectsDuplicateKeyInsideBatch(t *testing.T) {
	ctx := context.Background()
	repo := New(time.Minute, 3)

	err := repo.SubmitBatch(ctx, []model.Job{
		{ID: "dup-a", IdempotencyKey: "run-3/chunk-0", RenderPlan: []byte(`{"n":1}`)},
		{ID: "dup-b", IdempotencyKey: "run-3/chunk-0", RenderPlan: []byte(`{"n":2}`)},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate idempotency key") {
		t.Fatalf("want duplicate-idempotency-key rejection, got %v", err)
	}
	if stats := repo.Stats(ctx); stats.Pending != 0 {
		t.Fatalf("rejected batch was partially stored: %+v", stats)
	}
}

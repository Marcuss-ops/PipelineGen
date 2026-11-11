package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
)

// TestIdempotencyParityBetweenBackends runs the same idempotency scenarios
// against the real PostgreSQL repository and the in-memory repository and
// requires identical outcomes. The audit found the two diverging: memory
// overwrote the idem index and accepted batches PostgreSQL rejected. A
// contract test pinned to only one backend could never see that.
type idempotencyBackend struct {
	name string
	repo repository.JobRepository
}

func idempotencyBackends(t *testing.T) []idempotencyBackend {
	t.Helper()
	return []idempotencyBackend{
		{name: "postgres", repo: newRepo(t, 30*time.Second, 3)},
		{name: "memory", repo: memory.New(30*time.Second, 3)},
	}
}

func batchRepository(t *testing.T, repo repository.JobRepository) repository.BatchRepository {
	t.Helper()
	batch, ok := repo.(repository.BatchRepository)
	if !ok {
		t.Fatalf("%T does not implement repository.BatchRepository", repo)
	}
	return batch
}

func idempotentRepository(t *testing.T, repo repository.JobRepository) repository.IdempotencyRepository {
	t.Helper()
	idem, ok := repo.(repository.IdempotencyRepository)
	if !ok {
		t.Fatalf("%T does not implement repository.IdempotencyRepository", repo)
	}
	return idem
}

func TestIdempotencyParityBetweenBackends(t *testing.T) {
	ctx := context.Background()
	const plan = `{"schema":"renderinggen.overlay-plan.v1","rev":1}`

	t.Run("idempotent replay returns the canonical job", func(t *testing.T) {
		for _, backend := range idempotencyBackends(t) {
			t.Run(backend.name, func(t *testing.T) {
				idem := idempotentRepository(t, backend.repo)
				first := backend.name + "-first"
				canonical, created, err := idem.SubmitIdempotent(ctx, model.Job{
					ID: first, IdempotencyKey: "replay/" + backend.name, RenderPlan: []byte(plan),
				})
				if err != nil || !created {
					t.Fatalf("first idempotent submit: created=%t err=%v", created, err)
				}
				if canonical == nil || canonical.ID != first {
					t.Fatalf("canonical = %+v, want %s", canonical, first)
				}

				replay, created, err := idem.SubmitIdempotent(ctx, model.Job{
					ID: backend.name + "-second", IdempotencyKey: "replay/" + backend.name, RenderPlan: []byte(plan),
				})
				if err != nil {
					t.Fatalf("replay: %v", err)
				}
				if created {
					t.Fatal("replay minted a second job for the same key")
				}
				if replay == nil || replay.ID != first {
					t.Fatalf("replay resolved to %+v, want the canonical %s", replay, first)
				}
			})
		}
	})

	t.Run("submit reusing a stored key is rejected", func(t *testing.T) {
		for _, backend := range idempotencyBackends(t) {
			t.Run(backend.name, func(t *testing.T) {
				key := "submit/" + backend.name
				if err := backend.repo.Submit(ctx, model.Job{ID: backend.name + "-key-owner", IdempotencyKey: key, RenderPlan: []byte(plan)}); err != nil {
					t.Fatalf("seed submit: %v", err)
				}
				err := backend.repo.Submit(ctx, model.Job{ID: backend.name + "-key-conflict", IdempotencyKey: key, RenderPlan: []byte(plan)})
				if !errors.Is(err, repository.ErrJobExists) {
					t.Fatalf("reused key: want ErrJobExists, got %v", err)
				}
				if _, getErr := backend.repo.Get(ctx, backend.name+"-key-conflict"); !errors.Is(getErr, repository.ErrNotFound) {
					t.Fatalf("conflicting submit was stored: %v", getErr)
				}
			})
		}
	})

	t.Run("batch reusing a stored key rejects the whole family", func(t *testing.T) {
		for _, backend := range idempotencyBackends(t) {
			t.Run(backend.name, func(t *testing.T) {
				key := "batch/" + backend.name
				if err := backend.repo.Submit(ctx, model.Job{ID: backend.name + "-seed", IdempotencyKey: key, RenderPlan: []byte(plan)}); err != nil {
					t.Fatalf("seed submit: %v", err)
				}
				batch := batchRepository(t, backend.repo)
				anchorID := backend.name + "-anchor"
				childID := backend.name + "-anchor-chunk-0"
				err := batch.SubmitBatch(ctx, []model.Job{
					{ID: anchorID, RenderPlan: []byte(plan)},
					{ID: childID, ParentJobID: anchorID, ChunkIndex: 0,
						FrameRange: &model.FrameRange{Start: 0, End: 120}, IdempotencyKey: key, RenderPlan: []byte(plan)},
				})
				if !errors.Is(err, repository.ErrJobExists) {
					t.Fatalf("batch reusing a stored key: want ErrJobExists, got %v", err)
				}
				for _, id := range []string{anchorID, childID} {
					if _, getErr := backend.repo.Get(ctx, id); !errors.Is(getErr, repository.ErrNotFound) {
						t.Fatalf("rejected batch left %s behind: %v", id, getErr)
					}
				}
			})
		}
	})

	t.Run("duplicate key inside one batch is rejected", func(t *testing.T) {
		for _, backend := range idempotencyBackends(t) {
			t.Run(backend.name, func(t *testing.T) {
				key := "batch-internal/" + backend.name
				err := batchRepository(t, backend.repo).SubmitBatch(ctx, []model.Job{
					{ID: backend.name + "-dup-a", IdempotencyKey: key, RenderPlan: []byte(plan)},
					{ID: backend.name + "-dup-b", IdempotencyKey: key, RenderPlan: []byte(plan)},
				})
				if err == nil || !strings.Contains(err.Error(), "duplicate idempotency key") {
					t.Fatalf("want duplicate-idempotency-key rejection, got %v", err)
				}
			})
		}
	})

	t.Run("same id under a different key is a duplicate", func(t *testing.T) {
		for _, backend := range idempotencyBackends(t) {
			t.Run(backend.name, func(t *testing.T) {
				id := backend.name + "-same-id"
				if err := backend.repo.Submit(ctx, model.Job{ID: id, IdempotencyKey: "same-id-a/" + backend.name, RenderPlan: []byte(plan)}); err != nil {
					t.Fatalf("seed submit: %v", err)
				}
				_, created, err := idempotentRepository(t, backend.repo).SubmitIdempotent(ctx, model.Job{
					ID: id, IdempotencyKey: "same-id-b/" + backend.name, RenderPlan: []byte(plan),
				})
				if !errors.Is(err, repository.ErrJobExists) {
					t.Fatalf("same id/different key: want ErrJobExists, got %v", err)
				}
				if created {
					t.Fatal("conflicting identity was reported as created")
				}
			})
		}
	})
}

type batchIdempotent interface {
	SubmitBatchIdempotent(context.Context, []model.Job) (bool, error)
}

func TestBatchIdempotencyParityBetweenBackends(t *testing.T) {
	ctx := context.Background()
	for _, backend := range idempotencyBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			repo, ok := backend.repo.(batchIdempotent)
			if !ok {
				t.Fatalf("%T does not implement atomic batch idempotency", backend.repo)
			}
			family := []model.Job{
				{ID: backend.name + "-family", IdempotencyKey: backend.name + "-family-key", RenderPlan: []byte(`{"n":1}`)},
				{ID: backend.name + "-chunk-0", ParentJobID: backend.name + "-family", ChunkIndex: 0,
					FrameRange: &model.FrameRange{Start: 0, End: 120}, IdempotencyKey: backend.name + "-chunk-key", RenderPlan: []byte(`{"n":2}`)},
			}
			created, err := repo.SubmitBatchIdempotent(ctx, family)
			if err != nil || !created {
				t.Fatalf("first family submit: created=%t err=%v", created, err)
			}
			replayed, err := repo.SubmitBatchIdempotent(ctx, family)
			if err != nil || replayed {
				t.Fatalf("exact family replay: created=%t err=%v", replayed, err)
			}
			changed := append([]model.Job(nil), family...)
			changed[1].RenderPlan = []byte(`{"n":3}`)
			if _, err := repo.SubmitBatchIdempotent(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) {
				t.Fatalf("changed child replay: err=%v, want idempotency conflict", err)
			}
			if _, err := backend.repo.Get(ctx, backend.name+"-chunk-0"); err != nil {
				t.Fatalf("conflicting replay altered canonical child: %v", err)
			}
		})
	}
}

func TestBatchIdempotencyRejectsPartialExistingFamily(t *testing.T) {
	ctx := context.Background()
	for _, backend := range idempotencyBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			repo, ok := backend.repo.(batchIdempotent)
			if !ok {
				t.Fatalf("%T does not implement atomic batch idempotency", backend.repo)
			}
			anchor := model.Job{ID: backend.name + "-partial-anchor", IdempotencyKey: backend.name + "-partial-anchor-key", RenderPlan: []byte(`{"n":1}`)}
			if err := backend.repo.Submit(ctx, anchor); err != nil {
				t.Fatalf("seed partial anchor: %v", err)
			}
			family := []model.Job{
				anchor,
				{ID: backend.name + "-partial-child", ParentJobID: anchor.ID, ChunkIndex: 0,
					FrameRange: &model.FrameRange{Start: 0, End: 120}, IdempotencyKey: backend.name + "-partial-child-key", RenderPlan: []byte(`{"n":2}`)},
			}
			if created, err := repo.SubmitBatchIdempotent(ctx, family); created || !errors.Is(err, repository.ErrIdempotencyConflict) {
				t.Fatalf("partial family replay: created=%t err=%v, want idempotency conflict", created, err)
			}
			if _, err := backend.repo.Get(ctx, family[1].ID); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("partial replay inserted child: %v", err)
			}
		})
	}
}

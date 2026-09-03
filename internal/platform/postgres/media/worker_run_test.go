// Package media — worker_run_test.go: DSN-gated tests for the
// EnsureEmbeddingFamily bootstrap and the PostgresIndexWorker.Run drain
// loop (the production entry point launched by the composition root).
package media_test

import (
	"context"
	"errors"
	"testing"
	"time"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// TestEnsureEmbeddingFamily_BootstrapAndDrift pins the boot-time family
// contract: absent → registered; present + same dim → no-op; present +
// different dim → ErrFamilyDimDrift (never overwrite).
func TestEnsureEmbeddingFamily_BootstrapAndDrift(t *testing.T) {
	w, db := newVectorWriter(t)
	ctx := context.Background()

	// Absent → registered.
	if err := w.EnsureEmbeddingFamily(ctx, "text", "e5-prod", 768); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	// Present + same dim → idempotent no-op.
	if err := w.EnsureEmbeddingFamily(ctx, "text", "e5-prod", 768); err != nil {
		t.Fatalf("idempotent ensure: %v", err)
	}
	// Present + different dim → fail-closed drift.
	err := w.EnsureEmbeddingFamily(ctx, "text", "e5-prod", 384)
	if !errors.Is(err, pgmedia.ErrFamilyDimDrift) {
		t.Fatalf("dim drift err = %v, want ErrFamilyDimDrift", err)
	}
	// The stored dim was never clobbered by the failed overwrite.
	var dim int
	if err := db.QueryRow(`SELECT dim FROM media_embedding_families WHERE embedding_type='text' AND model_id='e5-prod'`).Scan(&dim); err != nil {
		t.Fatalf("read family: %v", err)
	}
	if dim != 768 {
		t.Fatalf("family dim = %d after drift attempt, want 768 (fail-closed)", dim)
	}
}

// noopLogger satisfies pgmedia.Logger without emitting anything.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

// TestWorker_Run_DrainsPendingEvents pins the production drain loop:
// pending events committed while the loop runs get embedded, indexed,
// and completed without any manual claim.
func TestWorker_Run_DrainsPendingEvents(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	if err := w.EnsureEmbeddingFamily(ctx, "text", "test-e5-worker-v1", 4); err != nil {
		t.Fatalf("EnsureEmbeddingFamily: %v", err)
	}
	emb := &stubEmbedder{fails: map[string]error{}}
	worker := pgmedia.NewPostgresIndexWorker(pgmedia.NewOutboxRepository(db), w, emb, "test-e5-worker-v1")

	// Start the drain loop with a fast cadence.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		worker.Run(runCtx, 20*time.Millisecond, time.Minute, noopLogger{})
		close(done)
	}()

	// Commit two indexable assets AFTER the loop is live: the loop must
	// pick both up without manual claims.
	seedIndexableAsset(t, db, "yt_run_a_v1")
	seedIndexableAsset(t, db, "yt_run_b_v1")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var completed int
		var pending int
		if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status='completed'`).Scan(&completed); err != nil {
			t.Fatalf("count completed: %v", err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND status IN ('pending','processing')`).Scan(&pending); err != nil {
			t.Fatalf("count pending: %v", err)
		}
		if completed == 2 && pending == 0 {
			// Both assets INDEXED via the loop.
			for _, id := range []string{"yt_run_a_v1", "yt_run_b_v1"} {
				var state string
				if err := db.QueryRow(`SELECT index_state FROM media_assets WHERE id=$1`, id).Scan(&state); err != nil {
					t.Fatalf("read %s: %v", id, err)
				}
				if state != "INDEXED" {
					t.Fatalf("%s index_state = %q, want INDEXED", id, state)
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not exit after ctx cancel")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatal("drain loop did not complete both events within deadline")
}

// TestWorker_Run_SurvivesPerEventFailures pins that a poison event does
// not kill the loop: the failing event retries/dead-letters while later
// healthy events still complete.
func TestWorker_Run_SurvivesPerEventFailures(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	if err := w.EnsureEmbeddingFamily(ctx, "text", "test-e5-worker-v1", 4); err != nil {
		t.Fatalf("EnsureEmbeddingFamily: %v", err)
	}
	emb := &stubEmbedder{fails: map[string]error{
		"yt_run_poison_v1": errors.New("embedder sidecar unavailable"),
	}}
	worker := pgmedia.NewPostgresIndexWorker(pgmedia.NewOutboxRepository(db), w, emb, "test-e5-worker-v1")

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go worker.Run(runCtx, 20*time.Millisecond, time.Minute, noopLogger{})

	seedIndexableAsset(t, db, "yt_run_poison_v1")
	seedIndexableAsset(t, db, "yt_run_healthy_v1")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var healthyCompleted int
		if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE event_type='asset.index.requested' AND aggregate_id='yt_run_healthy_v1' AND status='completed'`).Scan(&healthyCompleted); err != nil {
			t.Fatalf("count: %v", err)
		}
		if healthyCompleted == 1 {
			// The poison event must be in retry/backoff (or dead-lettered),
			// NOT completed — and the loop is still alive.
			var poisonStatus string
			if err := db.QueryRow(`SELECT status FROM outbox_events WHERE aggregate_id='yt_run_poison_v1'`).Scan(&poisonStatus); err != nil {
				t.Fatalf("read poison status: %v", err)
			}
			if poisonStatus == "completed" {
				t.Fatal("poison event must never complete")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("healthy event was not completed while poison event failed — loop may have died")
}

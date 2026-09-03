// Package media — outbox_worker_test.go: DSN-gated live tests for the
// PostgreSQL outbox consumption lifecycle and the canonical index worker
// (the pgvector replacement of the SQLite → Qdrant projection chain).
//
// Implements the POSTGRES-MEDIA-CUTOVER worker criteria:
//
//   - worker end-to-end: pending event → embed → vector upsert →
//     index_state=INDEXED → event completed
//   - idempotency: redelivered event converges to the same terminal state
//   - lease fencing: stale lease cannot complete/fail an event
//   - retry/dead-letter: failures requeue with backoff, exhaust to
//     dead_letter
//   - fail-closed: unknown asset is retried then dead-lettered, never
//     silently dropped
package media_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// stubEmbedder returns fixed vectors; failing assets are named in the map.
type stubEmbedder struct {
	vecs  map[string][]float32
	fails map[string]error
}

func (s *stubEmbedder) EmbedAssetText(_ context.Context, assetID string) ([]float32, error) {
	if err, ok := s.fails[assetID]; ok {
		return nil, err
	}
	if v, ok := s.vecs[assetID]; ok {
		return v, nil
	}
	return []float32{0.25, 0.25, 0.25, 0.25}, nil
}

func newWorkerFixture(t *testing.T) (*pgmedia.PostgresIndexWorker, *sql.DB, *stubEmbedder) {
	t.Helper()
	db := newMediaTestDB(t)
	ctx := context.Background()
	w := pgmedia.NewVectorSurfaceWriter(db)
	if err := w.RegisterEmbeddingFamily(ctx, "text", "test-e5-worker-v1", 4); err != nil {
		t.Fatalf("RegisterEmbeddingFamily: %v", err)
	}
	repo := pgmedia.NewOutboxRepository(db)
	emb := &stubEmbedder{fails: map[string]error{}}
	worker := pgmedia.NewPostgresIndexWorker(repo, w, emb, "test-e5-worker-v1")
	return worker, db, emb
}

// seedIndexableAsset commits one indexable asset through the canonical
// committer constructed on the SHARED db (a nested newMediaTestDB would
// truncate the family registry via its cleanup). Step 7 of the canonical
// commit emits asset.index.requested into the PG outbox.
func seedIndexableAsset(t *testing.T, db *sql.DB, assetID string) {
	t.Helper()
	box := pgmedia.NewOutboxRepository(db)
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	c := pgmedia.NewPostgresMediaCommitter(db, box, ledger, nil)
	req := fullCommitRequest()
	req.Asset.AssetID = assetID
	if _, err := c.CommitMediaAsset(context.Background(), req); err != nil {
		t.Fatalf("CommitMediaAsset: %v", err)
	}
}

func claimPendingEvent(t *testing.T, db *sql.DB) *pgmedia.OutboxClaim {
	t.Helper()
	repo := pgmedia.NewOutboxRepository(db)
	claim, err := repo.ClaimNext(context.Background(), "worker-test", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	return claim
}

// TestWorker_EndToEnd_EventToEmbeddingToIndexedToCompleted pins the full
// replacement chain in ONE database: commit → pending event → worker →
// embedding + INDEXED + completed. No Qdrant, no SQLite hop.
func TestWorker_EndToEnd_EventToEmbeddingToIndexedToCompleted(t *testing.T) {
	worker, db, _ := newWorkerFixture(t)
	ctx := context.Background()

	seedIndexableAsset(t, db, "yt_worker_e2e_v1")

	claim := claimPendingEvent(t, db)
	if claim == nil {
		t.Fatal("expected a pending asset.index.requested event after canonical commit")
	}
	if claim.Event.EventType != "asset.index.requested" {
		t.Fatalf("event type = %q, want asset.index.requested", claim.Event.EventType)
	}

	if err := worker.Handle(ctx, claim); err != nil {
		t.Fatalf("worker.Handle: %v", err)
	}

	// Vector written in the SSOT under the pinned model family.
	var dims int
	var modelID string
	if err := db.QueryRow(`SELECT vector_dims(embedding), model_id FROM media_embeddings WHERE asset_id = $1 AND embedding_type = 'text'`,
		"yt_worker_e2e_v1").Scan(&dims, &modelID); err != nil {
		t.Fatalf("embedding row missing: %v", err)
	}
	if dims != 4 || modelID != "test-e5-worker-v1" {
		t.Fatalf("embedding (dims=%d, model=%s), want (4, test-e5-worker-v1)", dims, modelID)
	}

	// Index state flipped in the same engine.
	var state string
	if err := db.QueryRow(`SELECT index_state FROM media_assets WHERE id = $1`, "yt_worker_e2e_v1").Scan(&state); err != nil {
		t.Fatalf("read index_state: %v", err)
	}
	if state != "INDEXED" {
		t.Fatalf("index_state = %q, want INDEXED", state)
	}

	// Event completed.
	var status string
	if err := db.QueryRow(`SELECT status FROM outbox_events WHERE id = $1`, claim.Event.ID).Scan(&status); err != nil {
		t.Fatalf("read event status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("event status = %q, want completed", status)
	}
}

// TestWorker_RedeliveryIsIdempotent pins convergence: handling a
// redelivered (freshly claimed) copy of the same event ends in the same
// terminal state with exactly one embedding row.
func TestWorker_RedeliveryIsIdempotent(t *testing.T) {
	worker, db, _ := newWorkerFixture(t)
	ctx := context.Background()

	seedIndexableAsset(t, db, "yt_worker_idem_v1")

	claim1 := claimPendingEvent(t, db)
	if err := worker.Handle(ctx, claim1); err != nil {
		t.Fatalf("first handle: %v", err)
	}
	if _, err := db.Exec(`UPDATE outbox_events SET status='pending', lease_id='', attempt_count=0 WHERE id=$1`, claim1.Event.ID); err != nil {
		t.Fatalf("requeue event: %v", err)
	}
	claim2 := claimPendingEvent(t, db)
	if err := worker.Handle(ctx, claim2); err != nil {
		t.Fatalf("redelivered handle: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_embeddings WHERE asset_id = $1`, "yt_worker_idem_v1").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("embedding rows = %d, want 1 (idempotent)", n)
	}
	var status string
	_ = db.QueryRow(`SELECT status FROM outbox_events WHERE id = $1`, claim2.Event.ID).Scan(&status)
	if status != "completed" {
		t.Fatalf("redelivered event status = %q, want completed", status)
	}
}

// TestWorker_LeaseFencing pins the fencing contract: a stale lease
// cannot complete an event that was re-claimed by another worker.
func TestWorker_LeaseFencing(t *testing.T) {
	_, db, _ := newWorkerFixture(t)
	ctx := context.Background()

	seedIndexableAsset(t, db, "yt_worker_fence_v1")
	claim := claimPendingEvent(t, db)
	repo := pgmedia.NewOutboxRepository(db)

	// A second worker steals the lease (simulating expiry + reclaim).
	if err := repo.MarkCompleted(ctx, claim.Event.ID, "stale-lease-id"); err == nil {
		t.Fatal("expected ErrLeaseLost for stale lease completion")
	}
	// The legitimate lease still completes.
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("legitimate MarkCompleted: %v", err)
	}
}

// TestWorker_FailureRetriesThenDeadLetters pins the retry contract:
// embed failures requeue the event; after max_attempts it dead-letters
// with the error recorded (never silently dropped).
func TestWorker_FailureRetriesThenDeadLetters(t *testing.T) {
	worker, db, emb := newWorkerFixture(t)
	ctx := context.Background()
	emb.fails["yt_worker_dead_v1"] = errors.New("embedder sidecar unavailable")

	seedIndexableAsset(t, db, "yt_worker_dead_v1")

	repo := pgmedia.NewOutboxRepository(db)
	// Exhaust the event's attempts (max_attempts default 10). Between
	// attempts, rewind next_attempt_at to simulate elapsed backoff —
	// ClaimNext honors the scheduled retry time.
	for i := 0; i < 12; i++ {
		claim, err := repo.ClaimNext(ctx, "worker-test", time.Minute)
		if err != nil {
			t.Fatalf("ClaimNext #%d: %v", i+1, err)
		}
		if claim == nil {
			// Expected when the event dead-lettered on a prior attempt:
			// ClaimNext only surfaces pending rows. Verify terminal state.
			var status, lastErr string
			var attempts int
			if err := db.QueryRow(`SELECT status, attempt_count, last_error FROM outbox_events WHERE event_type='asset.index.requested' AND aggregate_id='yt_worker_dead_v1'`).Scan(&status, &attempts, &lastErr); err != nil {
				t.Fatalf("event vanished at attempt %d and row unreadable: %v", i+1, err)
			}
			if status != "dead_letter" {
				t.Fatalf("event unclaimable at attempt %d but status=%s (attempts=%d), want dead_letter", i+1, status, attempts)
			}
			if lastErr == "" {
				t.Fatal("dead_letter row must record last_error (never silently dropped)")
			}
			break
		}
		if err := worker.Handle(ctx, claim); err == nil {
			t.Fatalf("expected embed failure on attempt %d", i+1)
		}
		if _, err := db.Exec(`UPDATE outbox_events SET next_attempt_at = '1970-01-01T00:00:00Z' WHERE id = $1`, claim.Event.ID); err != nil {
			t.Fatalf("rewind backoff: %v", err)
		}
	}
	// Event exhausted → dead_letter; asset remains un-indexed (no fake
	// availability), zero embedding rows.
	var status string
	if err := db.QueryRow(`SELECT status FROM outbox_events WHERE event_type='asset.index.requested' AND aggregate_id='yt_worker_dead_v1'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("status = %q, want dead_letter", status)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM media_embeddings WHERE asset_id='yt_worker_dead_v1'`).Scan(&n)
	if n != 0 {
		t.Fatalf("embedding rows = %d after dead-letter, want 0", n)
	}
	var state string
	_ = db.QueryRow(`SELECT index_state FROM media_assets WHERE id='yt_worker_dead_v1'`).Scan(&state)
	if state == "INDEXED" {
		t.Fatal("dead-lettered asset must not be INDEXED")
	}
}

// TestWorker_FailClosedOnUnknownAsset pins that a referenced-but-missing
// asset fails the worker (retried, then dead-lettered) rather than
// writing an orphan vector.
func TestWorker_FailClosedOnUnknownAsset(t *testing.T) {
	worker, db, _ := newWorkerFixture(t)
	ctx := context.Background()

	seedIndexableAsset(t, db, "yt_worker_missing_v1")
	// Simulate SSOT drift: the asset row vanished after the event.
	if _, err := db.Exec(`DELETE FROM media_assets WHERE id = 'yt_worker_missing_v1'`); err != nil {
		t.Fatalf("delete: %v", err)
	}

	claim := claimPendingEvent(t, db)
	if err := worker.Handle(ctx, claim); err == nil {
		t.Fatal("expected fail-closed error for unknown asset")
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM media_embeddings WHERE asset_id='yt_worker_missing_v1'`).Scan(&n)
	if n != 0 {
		t.Fatalf("orphan embedding rows = %d, want 0", n)
	}
}

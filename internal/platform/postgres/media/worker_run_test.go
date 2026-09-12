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

type recordingOutboxStatusMetrics struct {
	values    map[string]int64
	backlog   map[string]int64
	oldest    map[string]float64
	processed map[string]int64
}

func (m *recordingOutboxStatusMetrics) ObserveOutboxProcessed(eventType string) {
	if m.processed == nil {
		m.processed = make(map[string]int64)
	}
	m.processed[eventType]++
}

func (m *recordingOutboxStatusMetrics) ObserveOutboxStatus(eventType, status string, count int64) {
	if m.values == nil {
		m.values = make(map[string]int64)
	}
	m.values[eventType+"/"+status] = count
}

func (m *recordingOutboxStatusMetrics) ObserveOutboxBacklog(eventType string, backlogCount int64, oldestEventAgeSeconds float64) {
	if m.backlog == nil {
		m.backlog = make(map[string]int64)
	}
	if m.oldest == nil {
		m.oldest = make(map[string]float64)
	}
	m.backlog[eventType] = backlogCount
	m.oldest[eventType] = oldestEventAgeSeconds
}

// TestWorker_RefreshesObservedOutboxStatuses pins the operational metrics
// projection: the worker reads pending/dead_letter counts from the same
// PostgreSQL outbox rows used for delivery and emits zero for an absent
// status instead of leaving a stale gauge value behind.
func TestWorker_RefreshesObservedOutboxStatuses(t *testing.T) {
	worker, db, _ := newWorkerFixture(t)
	const eventType = "clip.render.drive_delivery.requested.v1"
	metrics := &recordingOutboxStatusMetrics{}
	worker.WithOutboxStatusMetrics(metrics, eventType)

	if _, err := db.Exec(`
		INSERT INTO outbox_events
		(event_type, aggregate_id, aggregate_type, payload_json, event_key,
		 status, attempt_count, max_attempts, created_at, updated_at,
		 created_at_ts, updated_at_ts)
		VALUES ($1, 'clip-metrics-v1', 'asset', '{}', 'clip-metrics-v1',
		 'pending', 0, 3, now(), now(), now(), now())
	`, eventType); err != nil {
		t.Fatalf("insert pending clip delivery intent: %v", err)
	}
	if err := worker.RefreshOutboxStatusMetrics(context.Background()); err != nil {
		t.Fatalf("refresh pending metrics: %v", err)
	}
	if got := metrics.values[eventType+"/pending"]; got != 1 {
		t.Fatalf("pending metric = %d, want 1", got)
	}
	if got := metrics.values[eventType+"/dead_letter"]; got != 0 {
		t.Fatalf("dead_letter metric = %d, want 0", got)
	}
	// Backlog + oldest-event age are projected by the same query so a
	// stalled drain is observable without a per-claim probe.
	if got := metrics.backlog[eventType]; got != 1 {
		t.Fatalf("backlog metric = %d, want 1", got)
	}
	if got := metrics.oldest[eventType]; got < 0 {
		t.Fatalf("oldest_event_age metric = %v, want >= 0", got)
	}

	if _, err := db.Exec(`UPDATE outbox_events SET status='dead_letter' WHERE event_key='clip-metrics-v1'`); err != nil {
		t.Fatalf("dead-letter clip delivery intent: %v", err)
	}
	if err := worker.RefreshOutboxStatusMetrics(context.Background()); err != nil {
		t.Fatalf("refresh dead-letter metrics: %v", err)
	}
	if got := metrics.values[eventType+"/pending"]; got != 0 {
		t.Fatalf("pending metric after terminal transition = %d, want 0", got)
	}
	if got := metrics.values[eventType+"/dead_letter"]; got != 1 {
		t.Fatalf("dead_letter metric after terminal transition = %d, want 1", got)
	}
	if got := metrics.backlog[eventType]; got != 0 {
		t.Fatalf("backlog metric after terminal transition = %d, want 0", got)
	}
	if got := metrics.oldest[eventType]; got != 0 {
		t.Fatalf("oldest_event_age after terminal transition = %v, want 0", got)
	}
}

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

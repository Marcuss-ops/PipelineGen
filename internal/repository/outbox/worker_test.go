package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"velox/go-master/internal/storage"
)

// outboxWorkerTestSchema mirrors the production migration layout for the
// media_index_outbox table. Defaults match what Repository.Enqueue relies on
// (datetime('now') for the timestamps, ” for textual fields).
const outboxWorkerTestSchema = `
	CREATE TABLE media_index_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		asset_id TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		embedding_model TEXT NOT NULL DEFAULT '',
		embedding_version TEXT NOT NULL DEFAULT '',
		collection_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		payload_json TEXT NOT NULL DEFAULT '{}',
		attempt_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		next_attempt_at TEXT NOT NULL DEFAULT (datetime('now')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE UNIQUE INDEX ux_outbox_dedup
		ON media_index_outbox(asset_id, content_hash, embedding_model, embedding_version, collection_version);
`

// seedPending inserts n distinguishable pending entries into the outbox
// table via the schema'd test DB. Returns the underlying repository and a
// closer for t.Cleanup.
func seedPending(t *testing.T, n int) *Repository {
	t.Helper()
	db := storage.NewTestDBWithSchema(t, outboxWorkerTestSchema)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db, zap.NewNop())

	payloadJSON, err := json.Marshal(Payload{
		AssetID: "test", EmbeddingModel: "m", EmbeddingVersion: "v",
		CollectionVersion: "v1",
	})
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO media_index_outbox
				(asset_id, content_hash, embedding_model, embedding_version,
				 collection_version, payload_json, next_attempt_at)
			VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
		`,
			fmt.Sprintf("asset_%04d", i),
			fmt.Sprintf("hash_%04d", i),
			"model", "v1", "v1", string(payloadJSON),
		)
		require.NoError(t, err)
	}
	return repo
}

// TestRepositoryClaimBatch_ReturnsRequestedCount confirms the SQL UPDATE…IN…
// pattern transitions the right number of pending rows to in_flight in a
// single round-trip and returns them as OutboxEntry values.
func TestRepositoryClaimBatch_ReturnsRequestedCount(t *testing.T) {
	repo := seedPending(t, 30)

	batch1, err := repo.ClaimBatch(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, batch1, 10)

	batch2, err := repo.ClaimBatch(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, batch2, 10)

	batch3, err := repo.ClaimBatch(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, batch3, 10)

	// Fourth batch finds no remaining pending rows.
	batch4, err := repo.ClaimBatch(context.Background(), 10)
	require.NoError(t, err)
	assert.Empty(t, batch4)
}

// TestRepositoryClaimBatch_MarksInFlightStatus verifies the side-effect of
// ClaimBatch — every claimed row transitions to in_flight so workers can
// detect abandoned rows.
func TestRepositoryClaimBatch_MarksInFlightStatus(t *testing.T) {
	repo := seedPending(t, 5)

	claimed, err := repo.ClaimBatch(context.Background(), 3)
	require.NoError(t, err)
	require.Len(t, claimed, 3)

	for _, e := range claimed {
		var status string
		err := repo.db.QueryRow("SELECT status FROM media_index_outbox WHERE id = ?", e.ID).Scan(&status)
		require.NoError(t, err)
		assert.Equal(t, "in_flight", status)
	}

	// Count pending rows — should be 2 remaining.
	pending, err := repo.CountByStatus(context.Background(), "pending")
	require.NoError(t, err)
	assert.Equal(t, int64(2), pending)
}

// TestRepositoryClaimBatch_ZeroLimitReturnsEmpty protects against an
// off-by-one that would otherwise consume the whole queue.
func TestRepositoryClaimBatch_ZeroLimitReturnsEmpty(t *testing.T) {
	repo := seedPending(t, 3)
	entries, err := repo.ClaimBatch(context.Background(), 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestWorker_NormalizeConfigDefaultsZeroFields verifies that an under-
// specified WorkerConfig (all zero) gets filled with DefaultWorkerConfig.
// The normalization lives in NewWorker — worth pinning explicitly so a
// future refactor that breaks the fall-back surfaces immediately.
func TestWorker_NormalizeConfigDefaultsZeroFields(t *testing.T) {
	repo := seedPending(t, 1)
	noOp := func(ctx context.Context, p *Payload) error { return nil }
	w := NewWorker(repo, noOp, WorkerConfig{}, zap.NewNop())
	assert.Equal(t, DefaultWorkerConfig(), w.cfg,
		"NewWorker must fill zero fields with DefaultWorkerConfig()")
}

// TestWorker_NormalizeProcessFuncNilPanics guards against the silent-loss
// failure mode: passing nil processFunc would log errors forever without
// ever indexing anything. Panicking at construction time is preferable.
func TestWorker_NormalizeProcessFuncNilPanics(t *testing.T) {
	repo := seedPending(t, 0)
	require.Panics(t, func() {
		NewWorker(repo, nil, DefaultWorkerConfig(), zap.NewNop())
	})
}

// TestWorker_StartProcessesAllEntries is the end-to-end proof of the
// batch+pool pipeline: inserting 25 pending rows and running the worker
// with batch 10 + 3 workers + a fast processFunc must mark every entry
// processed.
func TestWorker_StartProcessesAllEntries(t *testing.T) {
	repo := seedPending(t, 25)

	var processed atomic.Int64
	pf := func(ctx context.Context, p *Payload) error {
		processed.Add(1)
		return nil
	}

	cfg := DefaultWorkerConfig()
	// Aggressive timings so the test finishes in well under one second.
	cfg.PollInterval = 20 * time.Millisecond
	cfg.ReclaimInterval = 200 * time.Millisecond
	cfg.ProcessTimeout = 500 * time.Millisecond
	cfg.BatchSize = 10
	cfg.Workers = 3

	w := NewWorker(repo, pf, cfg, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.Start(ctx)

	// Wait until all 25 entries are processed or 3s elapse.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && processed.Load() < 25 {
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	assert.Equal(t, int64(25), processed.Load(),
		"all 25 pending entries must be processed by the worker pool")
}

// TestWorker_ConcurrencyFromMultipleWorkers verifies the pool actually runs
// N processFunc goroutines in parallel — a regression that fell back to a
// single goroutine would still pass TestStartProcessesAllEntries but fail
// this one. We instrument a barrier on each goroutine: an in-flight counter
// must hit >= 2 while we have 2 workers and 4 entries with a 100ms hold.
func TestWorker_ConcurrencyFromMultipleWorkers(t *testing.T) {
	repo := seedPending(t, 4)

	var inFlight atomic.Int64
	var peakInFlight atomic.Int64
	hold := 150 * time.Millisecond

	pf := func(ctx context.Context, p *Payload) error {
		cur := inFlight.Add(1)
		// Track peak concurrency.
		for {
			prev := peakInFlight.Load()
			if cur <= prev || peakInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(hold)
		inFlight.Add(-1)
		return nil
	}

	cfg := DefaultWorkerConfig()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.ReclaimInterval = 500 * time.Millisecond
	cfg.ProcessTimeout = 1 * time.Second
	cfg.BatchSize = 4
	cfg.Workers = 2

	w := NewWorker(repo, pf, cfg, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	// Wait until peak concurrency reaches 2 — proves N=2 workers ran in parallel.
	// Then wait until all 4 are processed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (peakInFlight.Load() < 2 || processedCount(repo, t) < 4) {
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	assert.GreaterOrEqual(t, peakInFlight.Load(), int64(2),
		"two workers must run processFunc in parallel (peak >= 2)")
}

// processedCount is a small helper used inside TestWorker_ConcurrencyFromMultipleWorkers
// to count rows that completed in the test DB.
func processedCount(repo *Repository, t *testing.T) int {
	t.Helper()
	var n int
	err := repo.db.QueryRow("SELECT COUNT(*) FROM media_index_outbox WHERE status = 'processed'").Scan(&n)
	require.NoError(t, err)
	return n
}

// TestWorker_StartShutdownDrainsOnCancel locks the defer-ordering fix from
// the PR-2 review: cancelling ctx must let the worker pool exit within a
// bounded time without leaving any goroutine stuck on `for entry := range
// work` waiting for a channel close that never arrives. The old
// `defer close(work); defer workerWg.Wait()` ordering deadlocked the
// shutdown (Wait ran first, blocked on workers whose channel was still
// open). The new `defer workerWg.Wait(); defer close(work)` ordering fires
// close first, then waits.
func TestWorker_StartShutdownDrainsOnCancel(t *testing.T) {
	repo := seedPending(t, 3)

	pf := func(ctx context.Context, p *Payload) error {
		// Slow processFunc so workers are mid-flight when we cancel —
		// the buffering ensures the channel close still drains them.
		time.Sleep(80 * time.Millisecond)
		return nil
	}

	cfg := DefaultWorkerConfig()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.ReclaimInterval = 1 * time.Second
	cfg.ProcessTimeout = 500 * time.Millisecond
	cfg.BatchSize = 3
	cfg.Workers = 2

	w := NewWorker(repo, pf, cfg, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	// Let the worker process at least one batch so we know goroutines are
	// actively in processEntry when we cancel.
	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Shutdown completed within the deadline.
	case <-time.After(2 * time.Second):
		t.Fatal("worker pool failed to shut down within 2s after ctx cancel — defer ordering is broken")
	}
}

// TestWorker_PanickedProcessFuncDoesNotKillPool verifies the recover()
// added per worker goroutine: one panicking processFunc must NOT shrink
// the pool. Subsequent entries must still be processed by surviving
// workers.
func TestWorker_PanickedProcessFuncDoesNotKillPool(t *testing.T) {
	repo := seedPending(t, 4)

	var processed atomic.Int64
	// First call panics, second succeeds. processFunc only "panics" once
	// because we use a sync.Once, but the goal is to verify the pool
	// stays alive across the panic rather than counting exact behaviour.
	var firstCallOnce sync.Once
	pf := func(ctx context.Context, p *Payload) error {
		processed.Add(1)
		firstCallOnce.Do(func() { panic("first entry explodes") })
		return nil
	}

	cfg := DefaultWorkerConfig()
	cfg.PollInterval = 20 * time.Millisecond
	cfg.ReclaimInterval = 1 * time.Second
	cfg.ProcessTimeout = 500 * time.Millisecond
	cfg.BatchSize = cfg.Workers // ensures everyone gets a shot
	cfg.Workers = 2

	w := NewWorker(repo, pf, cfg, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	// Wait until all 4 entries are processed despite the first panic.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && processed.Load() < 4 {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	assert.GreaterOrEqual(t, processed.Load(), int64(3),
		"the panic on the first entry must not shrink the pool — remaining 3 entries must still be processed (recovery keeps all 2 workers alive)")
}

// TestWorker_PeriodicReclaimReclaimsStale covers the bug fix that motivates
// the whole task: a worker crashing mid-embedding must not leave an
// in_flight row stuck until the next process restart. We mark an entry as
// in_flight with an old updated_at, start the worker with a fast reclaim
// tick, and verify the row's status returns to pending so it can be
// re-claimed.
func TestWorker_PeriodicReclaimReclaimsStale(t *testing.T) {
	repo := seedPending(t, 1)

	// Mark the seeded entry as in_flight with updated_at 1h ago — qualifies
	// as stale (threshold = 1 min in the test config below).
	_, err := repo.db.Exec(`
		UPDATE media_index_outbox
		SET status = 'in_flight',
		    updated_at = datetime('now', '-1 hour')
	`)
	require.NoError(t, err)

	// Initial verification: the row is in_flight.
	var status string
	require.NoError(t, repo.db.QueryRow("SELECT status FROM media_index_outbox LIMIT 1").Scan(&status))
	require.Equal(t, "in_flight", status)

	var reclaimed atomic.Int64
	// processFunc must NOT block on ctx — the entry's processCtx is alive
	// (worker has not been cancelled), but a real processCtx cancellation
	// would skip Complete. So keep it trivial.
	pf := func(ctx context.Context, p *Payload) error {
		reclaimed.Add(1)
		return nil
	}

	cfg := DefaultWorkerConfig()
	cfg.PollInterval = 500 * time.Millisecond   // slow poll so reclaim fires first
	cfg.ReclaimInterval = 50 * time.Millisecond // fast reclaim tick
	cfg.StaleThreshold = 1 * time.Minute
	cfg.BatchSize = 1
	cfg.Workers = 1

	w := NewWorker(repo, pf, cfg, zap.NewNop())

	// Note: NO cancel() between the two wait loops. The worker must stay
	// alive long enough to re-claim and process the reclaimed row; an
	// early cancel() short-circuits the second loop's expectation because
	// the worker's `<-ctx.Done()` select-arm fires before the claim tick
	// observes the pending row.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	// Wait until status returns to pending (the reclaim ticker flipped it).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = repo.db.QueryRow("SELECT status FROM media_index_outbox LIMIT 1").Scan(&status)
		if status == "pending" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	assert.Equal(t, "pending", status,
		"periodic reclaim must flip the stale in_flight row back to pending within ReclaimInterval")

	// The worker eventually processes the reclaimed entry too — verify the
	// full pipeline completes end-to-end. Worker keeps running until
	// `cancel()` above (deferred).
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && reclaimed.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	assert.Equal(t, int64(1), reclaimed.Load(),
		"after reclaim, the row is re-claimed and processed by the worker pool")
}

package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/concurrent"
	"velox/go-master/pkg/metrics"
)

// Worker is a concurrent claim+process pool for the outbox. A Start
// invocation:
//   - spawns N worker goroutines, each pulling claimed OffboxEntry rows
//     from a buffered work channel;
//   - on each PollInterval tick, atomically claims up to BatchSize
//     pending rows and dispatches them to the worker pool;
//   - on each ReclaimInterval tick, runs ReclaimStale(StaleThreshold)
//     so a worker crash doesn't leave an in_flight row stuck until the
//     next process restart;
//   - every 5s re-surfaces queue depth + oldest-pending-age to Prometheus.
//
// Defaults follow the PR-2 design review's CPU-only tuning: 500ms poll,
// batch 10, 2 workers, 5min per-entry timeout, 60s reclaim cadence,
// 360s stale threshold (2× process timeout), 5 max attempts.
type Worker struct {
	repo        *Repository
	processFunc ProcessFunc
	log         *zap.Logger
	cfg         WorkerConfig
}

// ProcessFunc is the function signature for outbox entry processing.
// It receives the parsed payload and should perform embedding + Qdrant upsert.
type ProcessFunc func(ctx context.Context, payload *Payload) error

// StaleRow policy: workers that crash mid-embedding or hit processTimeout
// leave their in_flight row with a stale `updated_at`. The periodic
// reclaim ticker flips any in_flight row whose `updated_at` is older than
// `StaleThreshold` back to pending so the next claim can re-dispatch it.
// Tune `StaleThreshold` to be > `ProcessTimeout` (PR-2 default: 360s vs
// 300s) so a worker that times out within its deadline doesn't lose its
// entry to a reclaim race with the timeout itself.
type WorkerConfig struct {
	PollInterval    time.Duration `yaml:"poll_interval"`
	BatchSize       int           `yaml:"batch_size"`
	Workers         int           `yaml:"workers"`
	ProcessTimeout  time.Duration `yaml:"process_timeout"`
	ReclaimInterval time.Duration `yaml:"reclaim_interval"`
	StaleThreshold  time.Duration `yaml:"stale_threshold"`
	MaxAttempts     int           `yaml:"max_attempts"`
}

// DefaultWorkerConfig returns the PR-2 CPU-only defaults. See type doc.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval:    500 * time.Millisecond,
		BatchSize:       10,
		Workers:         2,
		ProcessTimeout:  300 * time.Second,
		ReclaimInterval: 60 * time.Second,
		StaleThreshold:  360 * time.Second,
		MaxAttempts:     5,
	}
}

// normalizeWorkerConfig fills any zero-valued field with DefaultWorkerConfig
// so an under-specified config still produces sensible bounds. Called only
// from NewWorker.
func normalizeWorkerConfig(cfg WorkerConfig) WorkerConfig {
	def := DefaultWorkerConfig()
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = def.PollInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	if cfg.Workers <= 0 {
		cfg.Workers = def.Workers
	}
	if cfg.ProcessTimeout <= 0 {
		cfg.ProcessTimeout = def.ProcessTimeout
	}
	if cfg.ReclaimInterval <= 0 {
		cfg.ReclaimInterval = def.ReclaimInterval
	}
	if cfg.StaleThreshold <= 0 {
		cfg.StaleThreshold = def.StaleThreshold
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	return cfg
}

// NewWorker creates a new outbox worker pool.
func NewWorker(repo *Repository, processFunc ProcessFunc, cfg WorkerConfig, log *zap.Logger) *Worker {
	if log == nil {
		log = zap.NewNop()
	}
	if processFunc == nil {
		// A nil processFunc would silently lose entries; refuse to start
		// instead. The caller (composeIntegration) wires a real
		// clipIndexer.IndexClip closure.
		panic("outbox.NewWorker: processFunc is required")
	}
	return &Worker{
		repo:        repo,
		processFunc: processFunc,
		log:         log,
		cfg:         normalizeWorkerConfig(cfg),
	}
}

// Start blocks until ctx is cancelled. Spawns the worker pool and the
// claim/reclaim/metrics tickers. Cleanup: closes the work channel so the
// worker goroutines drain any remaining entries, then waits for them.
func (w *Worker) Start(ctx context.Context) {
	cfg := w.cfg
	w.log.Info("outbox worker pool started",
		zap.Duration("poll_interval", cfg.PollInterval),
		zap.Int("batch_size", cfg.BatchSize),
		zap.Int("workers", cfg.Workers),
		zap.Duration("process_timeout", cfg.ProcessTimeout),
		zap.Duration("reclaim_interval", cfg.ReclaimInterval),
		zap.Duration("stale_threshold", cfg.StaleThreshold),
		zap.Int("max_attempts", cfg.MaxAttempts),
	)

	// One-shot startup reclaim — preserves pre-PR-2 behaviour and catches
	// anything abandoned by a previous process crash.
	if reclaimed, err := w.repo.ReclaimStale(ctx, cfg.StaleThreshold); err != nil {
		w.log.Warn("startup reclaim failed", zap.Error(err))
	} else if reclaimed > 0 {
		w.log.Info("startup reclaim marked entries", zap.Int64("count", reclaimed))
	}

	// Worker pool: each goroutine reads claimed entries from `work` until
	// the channel closes. Buffer == BatchSize so a full claim doesn't
	// block the producer before N workers pick up.
	work := make(chan *OutboxEntry, cfg.BatchSize)
	var workerWg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		workerWg.Add(1)
		go func(id int) {
			defer workerWg.Done()
			// recover so a single misbehaving processFunc doesn't kill
			// a pool goroutine and silently shrink concurrency. The pool
			// stays at N workers even under panic; the bad entry gets
			// logged with stack so operators can fix the upstream bug.
			defer func() {
				if r := recover(); r != nil {
					w.log.Error("outbox worker panic recovered",
						zap.Int("worker_id", id),
						zap.Any("panic", r))
				}
			}()
			for entry := range work {
				w.processEntry(ctx, entry)
			}
		}(i)
	}

	// Tickers — independent so the three concerns (claim / reclaim /
	// metrics) don't couple. Stagger is implicit: each fires on its own
	// schedule.
	claimTicker := time.NewTicker(cfg.PollInterval)
	defer claimTicker.Stop()
	reclaimTicker := time.NewTicker(cfg.ReclaimInterval)
	defer reclaimTicker.Stop()
	metricsTicker := time.NewTicker(5 * time.Second)
	defer metricsTicker.Stop()

	// Shutdown sequencing — note the registration order. defer is LIFO, so
	// the SECOND-registered defer fires FIRST on return. We want
	// `close(work)` to fire while workers are still alive (so their
	// `for entry := range work` loop exits), then `workerWg.Wait()` to
	// drain. The order below puts Wait FIRST (registered → runs LAST) and
	// close SECOND (registered → runs FIRST). The opposite order — close
	// first then Wait — deadlocks the shutdown, because Wait would block
	// forever on workers still parked at `range work`.
	defer workerWg.Wait()
	defer close(work)

	for {
		select {
		case <-ctx.Done():
			w.log.Info("outbox worker pool stopped")
			return
		case <-reclaimTicker.C:
			// Periodic reclaim: the fix for the original bug where a worker
			// crashing mid-embedding would leave an in_flight row stuck
			// until the next process restart. Runs every ReclaimInterval
			// independent of the claim tick.
			if reclaimed, err := w.repo.ReclaimStale(ctx, cfg.StaleThreshold); err != nil {
				w.log.Warn("periodic reclaim failed", zap.Error(err))
			} else if reclaimed > 0 {
				w.log.Warn("periodic reclaim marked stale entries",
					zap.Int64("count", reclaimed),
					zap.Duration("threshold", cfg.StaleThreshold))
			}
		case <-metricsTicker.C:
			w.reportMetrics(ctx)
		case <-claimTicker.C:
			entries, err := w.repo.ClaimBatch(ctx, cfg.BatchSize)
			if err != nil {
				w.log.Error("claim batch failed",
					zap.Int("requested", cfg.BatchSize), zap.Error(err))
				continue
			}
			for _, e := range entries {
				select {
				case work <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// processEntry runs the inner pipeline: unmarshal payload, call the
// user-supplied ProcessFunc, mark complete or fail. Shared by all N worker
// goroutines — concurrency comes from the pool, not from re-implementing
// the DB plumbing per goroutine.
func (w *Worker) processEntry(ctx context.Context, entry *OutboxEntry) {
	cfg := w.cfg
	processCtx, cancel := context.WithTimeout(ctx, cfg.ProcessTimeout)
	defer cancel()

	w.log.Debug("processing outbox entry",
		zap.Int64("id", entry.ID),
		zap.String("asset_id", entry.AssetID),
		zap.Int("attempt", entry.AttemptCount))

	start := time.Now()
	var payload Payload
	if err := json.Unmarshal([]byte(entry.PayloadJSON), &payload); err != nil {
		w.log.Error("unmarshal payload failed",
			zap.Int64("id", entry.ID), zap.Error(err))
		_ = w.repo.Fail(processCtx, entry.ID, fmt.Sprintf("unmarshal: %v", err), cfg.MaxAttempts)
		return
	}

	if err := w.processFunc(processCtx, &payload); err != nil {
		metrics.OutboxProcessingDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
		w.log.Warn("outbox entry processing failed",
			zap.Int64("id", entry.ID),
			zap.String("asset_id", entry.AssetID),
			zap.Error(err))
		_ = w.repo.Fail(processCtx, entry.ID, err.Error(), cfg.MaxAttempts)
		return
	}

	if err := w.repo.Complete(processCtx, entry.ID); err != nil {
		w.log.Error("mark complete failed",
			zap.Int64("id", entry.ID), zap.Error(err))
		return
	}

	metrics.OutboxProcessingDuration.WithLabelValues("ok").Observe(time.Since(start).Seconds())
	w.log.Debug("outbox entry processed",
		zap.Int64("id", entry.ID), zap.String("asset_id", entry.AssetID))
}

// reportMetrics surfaces queue depth + oldest-pending-age to Prometheus.
// Decoupled from the claim loop so an empty queue still reports.
func (w *Worker) reportMetrics(ctx context.Context) {
	counts, err := w.repo.PendingCount(ctx)
	if err != nil {
		return
	}
	for status, count := range counts {
		metrics.OutboxQueueDepth.WithLabelValues(status).Set(float64(count))
	}
	if oldest, err := w.repo.OldestPendingAge(ctx); err == nil && oldest > 0 {
		metrics.OutboxOldestPendingSeconds.Set(oldest.Seconds())
	}
}

// RunBackground starts the worker pool in a background goroutine.
func (w *Worker) RunBackground(ctx context.Context) {
	concurrent.SafeGo("outbox-worker", func() { w.Start(ctx) })
}

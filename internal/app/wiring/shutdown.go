// Package app — graceful shutdown (PR4: takes *ComposeRoot).
//
// Before PR4 this file took *services + *backgroundJobs. After PR4 it takes
// *ComposeRoot + the backgroundJobs handle returned from
// go::startBackgroundJobs. The body is structurally identical
// (LIFO orchestration, 100ms settle, goroutine Stop with timeout, DB close)
// but consumes the new types.
package wiring

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// buildCleanup constructs a cleanup function that stops background jobs,
// waits for graceful shutdown, and closes the database.
//
// PR4a: Takes the assembled *ComposeRoot + the backgroundJobs handle +
// cancel + log. The signature replaces the previous
// `buildCleanup(dbs *Databases, jobs *backgroundJobs, cancel context.CancelFunc, log *zap.Logger)`.
//
// Orchestration order (LIFO):
//  1. cancel() — signals all goroutines
//  2. settle 100ms — give goroutines time to notice
//  3. parallel Stop for: channelMonitor
//  4. wait (5-second timeout)
//  5. close main database
//
// FASE 3.8 (July 2026): step 2 routes through `retry.Sleep`
// (Clock-injectable, ctx-aware) for consistency with the canonical
// retry-loop architecture; behavior-equivalent in production (100ms
// via RealClock). context.Background() is intentional — the parent
// ctx was just cancelled in step 1, so there is no parent ctx to
// honor.
func buildCleanup(dbs *Databases, root *ComposeRoot, jobs *backgroundJobs, cancel context.CancelFunc, log *zap.Logger) CleanupFunc {
	return func() {
		// 1. Cancel parent context to signal all background jobs to stop
		if cancel != nil {
			cancel()
		}

		// 2. Give jobs a moment to stop. Settle-drain is best-effort;
		// the outer 5-second cap in step 4 still bounds total cleanup
		// latency. context.Background() is intentional because the
		// parent ctx was just cancelled in step 1.
		const settleDrainTimeout = 100 * time.Millisecond
		// FASE 3.8: error path is unreachable in production (Background
		// never cancels); _ = is intentional for future migration if a
		// parent ctx is ever honored here. See Commit 2 audit-trail
		// notes for the broader pre-existing-breakage context.
		_ = retry.Sleep(context.Background(), settleDrainTimeout, retry.Options{})

		// 3. Stop services in parallel
		var wg sync.WaitGroup

		// Channel monitor shutdown (June 2026, Wave B): the scheduler loop
		// exits naturally via the parent ctx cancel in step 1 above (its
		// Start select has `<-ctx.Done()` as one of its cases). No explicit
		// Stop side-channel is needed — the cancellation-driven path is the
		// canonical 
		//
		// PR4.E-followup-2: explicit Stop for the outbox-events pool started
		// in go. We do NOT rely on outboxevents.Pool's internal
		// ctx.Done handling so in-flight work is drained gracefully even if
		// the pool implementation loses that behaviour in the future.
		//
		// Timeout strategy: the outer wg.Wait has a 5 s budget. We pass a
		// 4 s timeout to eventsPool.Stop so the inner drain can complete
		// (with up to ~1 s margin to the outer cap) and we still benefit
		// from an early return if the pool drains faster. Picking 15 s here
		// would be unreachable — the outer cap would always fire first.
		// VO-DECOMPOSITION P0 #1 (July 2026): stop persistent workers
		// so the python3 subprocesses don't leak on restart.
		// TTS worker (tts_edge_server.py).
		if root != nil && root.Domains != nil && root.Domains.AudioProcessor != nil {
			wg.Add(1)
			concurrent.SafeGo("cleanup-tts-worker", func() {
				defer wg.Done()
				if err := root.Domains.AudioProcessor.Stop(); err != nil {
					log.Warn("tts worker stop returned error", zap.Error(err))
				}
			})
		}
		// PR-ARGOS-TRANSLATION (Aug 2026): stop the persistent Argos
		// Translate sidecar so its python3 subprocess doesn't leak.
		if root != nil && root.TextTracks != nil && root.TextTracks.ArgosServer != nil {
			wg.Add(1)
			concurrent.SafeGo("cleanup-argos-server", func() {
				defer wg.Done()
				if err := root.TextTracks.ArgosServer.Stop(); err != nil {
					log.Warn("argos server stop returned error", zap.Error(err))
				}
			})
		}
		if root != nil && root.Outbox != nil && root.Outbox.EventsPool != nil {
			const eventsPoolStopTimeout = 4 * time.Second
			wg.Add(1)
			concurrent.SafeGo("cleanup-outbox-events-pool", func() {
				defer wg.Done()
				if err := root.Outbox.EventsPool.Stop(eventsPoolStopTimeout); err != nil {
					log.Warn("outbox events pool stop returned error", zap.Error(err))
				}
			})
		}

		// 4. Wait for all stop operations with timeout
		done := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("panic in cleanup wait goroutine", zap.Any("recover", r))
				}
				close(done)
			}()
			wg.Wait()
		}()
		select {
		case <-done:
			log.Info("All background jobs stopped")
		case <-time.After(5 * time.Second):
			log.Warn("Timeout waiting for background jobs to stop")
		}

		// 5. Close database connection
		if dbs.Main != nil {
			if err := dbs.Main.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
	}
}

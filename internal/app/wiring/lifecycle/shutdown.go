package lifecycle

import (
	"context"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// ShutdownDeps contains only the concrete stop/close operations required by
// lifecycle shutdown. The composition root maps its bundles into these narrow
// callbacks and does not leak ComposeRoot into this package.
type ShutdownDeps struct {
	AudioStop      func() error
	ArgosStop      func() error
	EventsPoolStop func(timeout time.Duration) error
	CloseMainDB    func() error
}

// BuildCleanup returns the canonical graceful-shutdown closure.
func BuildCleanup(deps ShutdownDeps, cancel context.CancelFunc, log *zap.Logger) func() {
	return func() {
		if cancel != nil {
			cancel()
		}

		const settleDrainTimeout = 100 * time.Millisecond
		_ = retry.Sleep(context.Background(), settleDrainTimeout, retry.Options{})

		var wg sync.WaitGroup
		if deps.AudioStop != nil {
			wg.Add(1)
			concurrent.SafeGo("cleanup-tts-worker", func() {
				defer wg.Done()
				if err := deps.AudioStop(); err != nil && log != nil {
					log.Warn("tts worker stop returned error", zap.Error(err))
				}
			})
		}
		if deps.ArgosStop != nil {
			wg.Add(1)
			concurrent.SafeGo("cleanup-argos-server", func() {
				defer wg.Done()
				if err := deps.ArgosStop(); err != nil && log != nil {
					log.Warn("argos server stop returned error", zap.Error(err))
				}
			})
		}
		if deps.EventsPoolStop != nil {
			const eventsPoolStopTimeout = 4 * time.Second
			wg.Add(1)
			concurrent.SafeGo("cleanup-outbox-events-pool", func() {
				defer wg.Done()
				if err := deps.EventsPoolStop(eventsPoolStopTimeout); err != nil && log != nil {
					log.Warn("outbox events pool stop returned error", zap.Error(err))
				}
			})
		}

		done := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil && log != nil {
					log.Error("panic in cleanup wait goroutine", zap.Any("recover", r))
				}
				close(done)
			}()
			wg.Wait()
		}()
		select {
		case <-done:
			if log != nil {
				log.Info("All background jobs stopped")
			}
		case <-time.After(5 * time.Second):
			if log != nil {
				log.Warn("Timeout waiting for background jobs to stop")
			}
		}

		if deps.CloseMainDB != nil {
			if err := deps.CloseMainDB(); err != nil && log != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
	}
}

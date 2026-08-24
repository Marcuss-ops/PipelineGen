// Package app — voiceover_publish_pool.go implements
// voiceover.AsyncPublishPool with a bounded goroutine pool so Drive
// uploads + timing publishes + SQLite commits never overwhelm the
// network or the DB connection pool.
//
// P0.4 (separate TTS pool from publish pool): when wired, the
// TTS slot is freed after synthesis (Stage 1+2) and the heavy I/O
// stages (Stage 3 Drive upload + timing, Stage 4 SQLite finalize)
// run in this pool's background goroutines. The runner calls Wait()
// after the voiceover phase drains (before audio compile) so Drive
// links are hydrated before downstream stages consume them.
package wiring

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// voiceoverPublishPool implements voiceover.AsyncPublishPool with a
// bounded semaphore (channel-based) and a WaitGroup for draining.
// Submit accepts work until the channel is full; a full channel
// means the pool is saturated and the caller should tune the
// concurrency cap.
type voiceoverPublishPool struct {
	sem concurrent.Semaphore
	wg  sync.WaitGroup
	log *zap.Logger
}

// NewVoiceoverPublishPool creates a bounded publish pool. concurrency
// caps the number of concurrent Drive uploads + DB commits. A value
// ≤0 defaults to 4 (reasonable default for I/O-bound work).
func NewVoiceoverPublishPool(concurrency int, log *zap.Logger) voiceover.AsyncPublishPool {
	if concurrency <= 0 {
		concurrency = 4
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &voiceoverPublishPool{
		sem: concurrent.NewSemaphore(concurrency),
		log: log,
	}
}

// Submit enqueues the publish+finalize work in a background goroutine.
// The semaphore bounds concurrency; a full semaphore means the pool is
// saturated — the work is still accepted but it will wait for a slot.
// This is intentional: blocking the Submit caller (which is the TTS
// worker returning from Execute) would defeat the purpose of freeing
// the TTS slot, so Submit always accepts immediately (the bounded
// semaphore queues via Go's channel buffer).
func (p *voiceoverPublishPool) Submit(_ context.Context, fn func()) {
	p.wg.Add(1)
	go func() {
		p.sem.Acquire()
		defer p.sem.Release()
		defer p.wg.Done()
		fn()
	}()
}

// Wait blocks until all submitted tasks complete. The runner calls
// this after the voiceover phase to ensure Drive links are hydrated
// before audio compile and docs stages.
func (p *voiceoverPublishPool) Wait() {
	p.wg.Wait()
}

// Compile-time assertion.
var _ voiceover.AsyncPublishPool = (*voiceoverPublishPool)(nil)

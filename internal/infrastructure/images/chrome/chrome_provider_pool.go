package images

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// ChromeImageProviderPool fans requests out across multiple independent
// ChromeImageProvider instances. Each provider owns its own Playwright
// process, so concurrent Generate calls can execute in parallel instead of
// serialising behind one browser mutex.
type ChromeImageProviderPool struct {
	providers []*ChromeImageProvider
	next      atomic.Uint64
	log       *zap.Logger
}

var _ ImageGenerator = (*ChromeImageProviderPool)(nil)

// NewChromeImageProviderPool constructs a pool of independent Chrome workers.
// poolSize is clamped to at least 1.
func NewChromeImageProviderPool(scriptsDir string, poolSize int, log *zap.Logger) *ChromeImageProviderPool {
	return NewChromeImageProviderPoolFromProfile(scriptsDir, poolSize, 0, log)
}

// NewChromeImageProviderPoolFromProfile constructs a pool whose first worker
// uses profileID. This lets operators select an authenticated persistent
// Google Slides profile while preserving the default 0-based pool behavior.
func NewChromeImageProviderPoolFromProfile(scriptsDir string, poolSize, profileID int, log *zap.Logger) *ChromeImageProviderPool {
	if poolSize < 1 {
		poolSize = 1
	}
	pool := &ChromeImageProviderPool{
		providers: make([]*ChromeImageProvider, 0, poolSize),
		log:       log,
	}
	for i := 0; i < poolSize; i++ {
		pool.providers = append(pool.providers, NewChromeImageProvider(scriptsDir, profileID+i, log))
	}
	return pool
}

// Generate routes the request to the next provider in round-robin order.
// Each underlying provider keeps its own mutex and worker process, so
// concurrent calls can proceed in parallel across the pool.
func (p *ChromeImageProviderPool) Generate(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error) {
	if p == nil || len(p.providers) == 0 {
		return nil, fmt.Errorf("chrome provider pool is empty")
	}
	idx := int(p.next.Add(1)-1) % len(p.providers)
	return p.providers[idx].Generate(ctx, req)
}

// TriggerPrewarm warms up the requested number of providers, up to the pool
// size. Warmup is performed in parallel so the browser processes are ready
// before the fan-out starts.
func (p *ChromeImageProviderPool) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if p == nil || len(p.providers) == 0 {
		return
	}
	if count < 1 {
		count = 1
	}
	if count > len(p.providers) {
		count = len(p.providers)
	}

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		pw := p.providers[i]
		go func(profile int, worker *ChromeImageProvider) {
			defer wg.Done()
			if err := worker.Warmup(ctx); err != nil && p.log != nil {
				p.log.Warn("chrome pool prewarm failed",
					zap.String("job_id", jobID),
					zap.Int("profile", profile),
					zap.Error(err))
			}
		}(i, pw)
	}
	wg.Wait()
}

// Stop shuts down every browser worker in the pool.
func (p *ChromeImageProviderPool) Stop() error {
	if p == nil {
		return nil
	}
	var firstErr error
	for i, worker := range p.providers {
		if worker == nil {
			continue
		}
		if err := worker.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("chrome pool worker %d stop: %w", i, err)
		}
	}
	return firstErr
}

// Health reports whether the pool has at least one responsive worker.
func (p *ChromeImageProviderPool) Health() error {
	if p == nil || len(p.providers) == 0 {
		return fmt.Errorf("chrome provider pool is empty")
	}
	for _, worker := range p.providers {
		if worker == nil {
			continue
		}
		if err := worker.Health(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no healthy chrome workers in pool")
}

// ActiveCooldownProfiles returns the count of workers that currently report
// an unhealthy state. It is used only for diagnostics and remains truthful
// for pooled workers.
func (p *ChromeImageProviderPool) ActiveCooldownProfiles() int {
	if p == nil {
		return 0
	}
	count := 0
	for _, worker := range p.providers {
		if worker == nil {
			continue
		}
		if err := worker.Health(); err != nil {
			count++
		}
	}
	return count
}

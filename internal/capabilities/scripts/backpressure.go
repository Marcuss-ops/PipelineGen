// Package scriptgeneration — backpressure.go owns the VidRush backpressure
// contract: separate concurrency limits for the three pipeline stages (entity
// extraction, provider search, materialization) and a single-slot generation
// priority gate so text generation is never starved by entity extraction when
// both share the same local single-slot Ollama model.
package scriptgeneration

import (
	"context"
	"sync"
)

// VidRushBackpressure holds the independent concurrency limits for the three
// VidRush stages. Keeping them separate means a slow stage (e.g. a provider
// download) can never consume the whole budget of a faster stage (e.g. entity
// extraction). Values <= 0 fall back to the stage default.
type VidRushBackpressure struct {
	// ExtractionLimit bounds concurrent entity-extraction calls. The local
	// Ollama model is single-slot, so the default is 1.
	ExtractionLimit int
	// ProviderSearchLimit bounds concurrent provider fan-out searches
	// (Artlist, internet images) after entities are ready. Default 4.
	ProviderSearchLimit int
	// MaterializationLimit bounds concurrent acquire/verify/finalize work.
	// Default 2.
	MaterializationLimit int
}

// resolved returns the effective limits, applying the stage defaults for any
// non-positive value.
func (b VidRushBackpressure) resolved() VidRushBackpressure {
	if b.ExtractionLimit <= 0 {
		b.ExtractionLimit = 1
	}
	if b.ProviderSearchLimit <= 0 {
		b.ProviderSearchLimit = 4
	}
	if b.MaterializationLimit <= 0 {
		b.MaterializationLimit = 2
	}
	return b
}

// DefaultVidRushBackpressure returns the canonical stage limits: extraction is
// single-slot (local Ollama), provider search is bounded at 4, and
// materialization at 2.
func DefaultVidRushBackpressure() VidRushBackpressure {
	return VidRushBackpressure{}.resolved()
}

// GenerationGate is a single-slot priority gate shared by scene generation
// (high priority) and VidRush entity extraction (low priority) when both use
// the same single-slot local Ollama model. A high-priority acquisition jumps
// the queue ahead of waiting low-priority acquisitions, so text generation is
// never starved by concurrent entity extraction; extraction fills the gaps
// between generation calls instead of competing with them.
type GenerationGate struct {
	mu       sync.Mutex
	held     bool
	highWait []chan struct{}
	lowWait  []chan struct{}
}

// NewGenerationGate constructs an empty (unheld) priority gate.
func NewGenerationGate() *GenerationGate {
	return &GenerationGate{}
}

// AcquireHigh blocks until the slot is available, with priority over any
// low-priority waiters. It returns ctx.Err() if the context is cancelled while
// waiting. Use this for scene generation.
func (g *GenerationGate) AcquireHigh(ctx context.Context) error {
	return g.acquire(ctx, true)
}

// AcquireLow blocks until the slot is available, behind any high-priority
// waiters. It returns ctx.Err() if the context is cancelled while waiting. Use
// this for entity extraction.
func (g *GenerationGate) AcquireLow(ctx context.Context) error {
	return g.acquire(ctx, false)
}

// Release returns the slot to the pool. The slot is granted to the next
// high-priority waiter if any, otherwise the next low-priority waiter;
// otherwise the gate becomes unheld. Release on an unheld gate is a no-op.
func (g *GenerationGate) Release() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var next chan struct{}
	switch {
	case len(g.highWait) > 0:
		next = g.highWait[0]
		g.highWait = g.highWait[1:]
	case len(g.lowWait) > 0:
		next = g.lowWait[0]
		g.lowWait = g.lowWait[1:]
	default:
		g.held = false
		return
	}
	// Hand the slot to the selected waiter; it becomes the new holder.
	close(next)
}

func (g *GenerationGate) acquire(ctx context.Context, high bool) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if !g.held {
		g.held = true
		g.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	if high {
		g.highWait = append(g.highWait, ch)
	} else {
		g.lowWait = append(g.lowWait, ch)
	}
	g.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		// Cancelled while waiting: drop our queued slot and hand it forward so
		// the gate never leaks a pending waiter.
		g.mu.Lock()
		remove := func(q []chan struct{}, target chan struct{}) []chan struct{} {
			for i, c := range q {
				if c == target {
					return append(q[:i], q[i+1:]...)
				}
			}
			return q
		}
		if high {
			g.highWait = remove(g.highWait, ch)
		} else {
			g.lowWait = remove(g.lowWait, ch)
		}
		g.mu.Unlock()
		return ctx.Err()
	}
}

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

// ── Certified worker-pool defaults ────────────────────────────────────
//
// The script-generation DAG fans out two independent branches from the
// SceneTextReady boundary: the NLP/entity branch (VidRush extraction) and
// the TTS voiceover branch. Both run through bounded shared worker pools;
// these certified defaults keep each branch parallel without unbounded
// rate-limit or CPU contention. Docs publishing and the Rust final-audio
// render stay single-threaded (concurrency 1) and are not governed here.

// DefaultGenerationConcurrency bounds the certified default for the LLM
// script-text generation branch: up to this many scene Ollama calls run
// concurrently. It matches the measured OLLAMA_NUM_PARALLEL baseline on the
// A4000/e4b server (3); the client gate must not exceed it because extra
// client slots only deepen the server-side queue (queue wait) without adding
// throughput.
const DefaultGenerationConcurrency = 3

// DefaultNLPConcurrency bounds the certified default for the NLP/entity
// extraction branch: up to this many scenes enrich concurrently.
const DefaultNLPConcurrency = 4

// DefaultTTSConcurrency bounds the certified default for the TTS voiceover
// branch: up to this many scene×language synthesis calls run concurrently.
// 4 is the measured optimum: widening the pool to 8 on the live 5-scene
// certification job raised provider-side contention (TTS work 32.3s → 41.0s,
// job wall 51.2s → 58.2s), so the certified default stays at 4.
const DefaultTTSConcurrency = 4

// DefaultTranslationConcurrency bounds concurrent scene×language translation
// calls. Results are applied and checkpointed in canonical order.
const DefaultTranslationConcurrency = 4

// VidRushBackpressure holds the independent concurrency limits for the three
// VidRush stages. Keeping them separate means a slow stage (e.g. a provider
// download) can never consume the whole budget of a faster stage (e.g. entity
// extraction). Values <= 0 fall back to the stage default.
type VidRushBackpressure struct {
	// ExtractionLimit bounds concurrent entity-extraction calls. The default
	// is the certified NLP concurrency (DefaultNLPConcurrency).
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
		b.ExtractionLimit = DefaultNLPConcurrency
	}
	if b.ProviderSearchLimit <= 0 {
		b.ProviderSearchLimit = 4
	}
	if b.MaterializationLimit <= 0 {
		b.MaterializationLimit = 2
	}
	return b
}

// DefaultVidRushBackpressure returns the canonical stage limits: extraction
// bounded at the certified NLP concurrency, provider search at 4, and
// materialization at 2.
func DefaultVidRushBackpressure() VidRushBackpressure {
	return VidRushBackpressure{}.resolved()
}

// GenerationGate is a capacity-bounded priority gate. Production wiring uses
// one instance for scene generation and a separate instance for VidRush entity
// extraction; the priority API remains useful for callers that intentionally
// share a provider gate. A high-priority acquisition jumps the queue ahead of
// waiting low-priority acquisitions. The default capacity is 1 (single-slot
// model); a capacity of N admits N concurrent holders.
type GenerationGate struct {
	mu       sync.Mutex
	capacity int
	held     int
	highWait []chan struct{}
	lowWait  []chan struct{}
}

// NewGenerationGate constructs an empty single-slot priority gate. Use
// NewGenerationGateWithCapacity for a bounded pool larger than one.
func NewGenerationGate() *GenerationGate {
	return NewGenerationGateWithCapacity(1)
}

// NewGenerationGateWithCapacity constructs an empty priority gate with the
// given number of slots. Values <= 0 fall back to a single slot.
func NewGenerationGateWithCapacity(capacity int) *GenerationGate {
	if capacity <= 0 {
		capacity = 1
	}
	return &GenerationGate{capacity: capacity}
}

// Capacity returns the configured hard ceiling for high-priority Ollama work.
func (g *GenerationGate) Capacity() int {
	if g == nil || g.capacity <= 0 {
		return 1
	}
	return g.capacity
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

// Release returns one slot to the pool. The slot is granted to the next
// high-priority waiter if any, otherwise the next low-priority waiter;
// otherwise the held count is decremented. Release when no slot is held and
// no waiter is queued is a no-op.
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
		if g.held > 0 {
			g.held--
		}
		return
	}
	// Hand the released slot directly to the selected waiter; held is
	// unchanged because the slot is transferred, not freed.
	close(next)
}

func (g *GenerationGate) acquire(ctx context.Context, high bool) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.held < g.capacity {
		g.held++
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

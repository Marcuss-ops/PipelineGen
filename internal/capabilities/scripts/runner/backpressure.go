// Package scriptgeneration — backpressure.go owns the VidRush backpressure
// contract: separate concurrency limits for semantic extraction, provider
// search and materialization, plus a bounded scene-generation model gate.
package runner

import (
	"context"
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

// MaxVidRushImageConcurrency caps image provider and materialization work at
// fifteen concurrent scene operations.
const MaxVidRushImageConcurrency = 15

// DefaultVidRushImageConcurrency allows up to fifteen independent scene
// searches/materializations to overlap. Image work is network-bound; its
// image-search calls still share a process-wide cap of the same size.
const DefaultVidRushImageConcurrency = MaxVidRushImageConcurrency

// DefaultTTSConcurrency bounds the certified default for the TTS voiceover
// branch: up to this many scene×language synthesis calls run concurrently.
// 4 is the measured optimum: widening the pool to 8 on the live 5-scene
// certification job raised provider-side contention (TTS work 32.3s → 41.0s,
// job wall 51.2s → 58.2s), so the certified default stays at 4.
const DefaultTTSConcurrency = 4

// MaxTranslationConcurrency caps concurrent scene×language translation
// calls. Results are applied and checkpointed in canonical order.
const MaxTranslationConcurrency = 4

// DefaultTranslationConcurrency is the translation pool width when no
// explicit value is configured. It intentionally equals the hard cap.
const DefaultTranslationConcurrency = MaxTranslationConcurrency

// DefaultOverlayRenderConcurrency bounds the multilingual overlay render
// fan-out: how many per-language OverlayPlans the overlay_render phase may
// have in flight against RenderingGen at once.
//
// The phase used to be a serial per-language loop (`for plan { enqueue; wait }`),
// so N languages cost N sequential Chronon round-trips and the wall time grew as
// Ttotal ≈ T1 + T2 + ... + TN. Flipping that to a bounded fan-out is the single
// largest wall-clock lever in a multilingual run.
//
// 2 is the deliberate FIRST step, not the ceiling: it overlaps the submit/wait
// of two languages while staying inside the worker's gpu_lanes=2 domain, and it
// must be certified for byte-determinism before an operator raises it. This is a
// PIPELINING bound, not a GPU bound — the RenderingGen worker remains the only
// authority on how many renders touch the device.
const DefaultOverlayRenderConcurrency = 2

// VidRushBackpressure holds the independent concurrency limits for the three
// VidRush stages. Keeping them separate means a slow stage (e.g. a provider
// download) can never consume the whole budget of a faster stage (e.g. entity
// extraction). Values <= 0 fall back to the stage default.
type VidRushBackpressure struct {
	// ExtractionLimit bounds concurrent entity-extraction calls. The default
	// is the certified NLP concurrency (DefaultNLPConcurrency).
	ExtractionLimit int
	// ProviderSearchLimit bounds concurrent provider fan-out searches
	ProviderSearchLimit int
	// MaterializationLimit bounds concurrent acquire/verify/finalize work.
	// Default 2.
	MaterializationLimit int
}

// resolved returns the effective limits, applying the stage defaults for any
// non-positive value.
func (b VidRushBackpressure) Resolved() VidRushBackpressure {
	if b.ExtractionLimit <= 0 {
		b.ExtractionLimit = DefaultNLPConcurrency
	}
	if b.ProviderSearchLimit <= 0 {
		b.ProviderSearchLimit = DefaultVidRushImageConcurrency
	} else if b.ProviderSearchLimit > MaxVidRushImageConcurrency {
		b.ProviderSearchLimit = MaxVidRushImageConcurrency
	}
	if b.MaterializationLimit <= 0 {
		b.MaterializationLimit = DefaultVidRushImageConcurrency
	} else if b.MaterializationLimit > MaxVidRushImageConcurrency {
		b.MaterializationLimit = MaxVidRushImageConcurrency
	}
	return b
}

// DefaultVidRushBackpressure returns the canonical stage limits: extraction
// bounded at the certified NLP concurrency, with provider search and image
// materialization each capped at DefaultVidRushImageConcurrency.
func DefaultVidRushBackpressure() VidRushBackpressure {
	return VidRushBackpressure{}.Resolved()
}

// GenerationGate bounds concurrent scene-generation calls to the local model.
// A capacity of N admits N concurrent holders.
type GenerationGate struct {
	slots chan struct{}
}

// NewGenerationGate constructs a single-slot model gate. Use
// NewGenerationGateWithCapacity for a bounded pool larger than one.
func NewGenerationGate() *GenerationGate {
	return NewGenerationGateWithCapacity(1)
}

// NewGenerationGateWithCapacity constructs a model gate with the given number
// of slots. Values <= 0 fall back to a single slot.
func NewGenerationGateWithCapacity(capacity int) *GenerationGate {
	if capacity <= 0 {
		capacity = 1
	}
	return &GenerationGate{slots: make(chan struct{}, capacity)}
}

// Capacity returns the configured hard ceiling.
func (g *GenerationGate) Capacity() int {
	if g == nil || g.slots == nil {
		return 1
	}
	return cap(g.slots)
}

// Acquire blocks until a scene-generation slot is available or ctx is
// cancelled.
func (g *GenerationGate) Acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}
	select {
	case g.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns one slot to the pool. Releasing an empty gate is a no-op.
func (g *GenerationGate) Release() {
	if g == nil {
		return
	}
	select {
	case <-g.slots:
	default:
	}
}

// Package scripts — cache_eviction_usecase is the use case for
// POST /api/script/cache/evict.
//
// Pre-PR4.F6 (June 2026) the orchestration (circuit-breaker reset,
// memory-cache eviction, nil-memory fallback, response shaping) lived
// inline in api/script/handler_flow_ops.go::ScriptFlowHandler.EvictCache.
// This 60-LOC handler mixed LLM-side concerns (ResetCircuitBreakers)
// with cache-side concerns (memoryCache (in-package interface).EvictExactOutputs) and
// branched three different ways on memorySvc presence. Moving the use
// case here makes the orchestration unit-testable without an HTTP
// context and removes the last imperative business branch from
// handler_flow_ops.go.
//
// The use case owns:
//   - reset LLM circuit breakers via Generator.GetClient().ResetCircuitBreakers
//     (always — even on empty title list)
//   - if titles provided AND Memory != nil → call Memory.EvictExactOutputs
//   - if titles provided AND Memory == nil → ErrCacheEvictionMissing
//   - if no titles → short-circuit with breaker-reset-only output
//
// The use case does NOT own:
//   - HTTP status codes (handler responsibility)
//   - JSON shape (handler responsibility)
//   - JSON-body parsing / EOF special case (handler responsibility)
//   - title trim/dedup (handler responsibility — closer to wire shape)
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
)

// ── Public input / output ───────────────────────────────────────────────────

// CacheEvictionInput is the use case input. Titles is the trimmed,
// non-empty list supplied by the HTTP layer (handler does
// strings.TrimSpace + empty-filter before reaching Run).
type CacheEvictionInput struct {
	Titles []string
}

// CacheEvictionOutput is the use case output.
//
//   - CircuitBreakersReset: count of breakers reset by
//     ResetCircuitBreakers. Useful for ops dashboards; the handler
//     surfaces it as the wire-level "models_reset" field.
//   - DeletedCount: rows deleted by Memory.EvictExactOutputs. Always 0
//     when Titles was empty (early-return path).
//   - EvictedTitles: the titles the memory service was asked to evict,
//     echoed back for caller log clarity. nil when Titles was empty.
//
// Wire-format note (PR4.F6, June 2026): the HTTP handler preserves the
// previous response shape (ok / deleted_count / evicted_titles /
// circuit_breakers:"reset") and ADDS the new "models_reset":<int>
// field. The addition is purely additive (no break for existing
// clients) but introduces a new post-refactor key that callers parsing
// strict schemas will see.
type CacheEvictionOutput struct {
	CircuitBreakersReset int
	DeletedCount         int64
	EvictedTitles        []string
}

// ── Errors ──────────────────────────────────────────────────────────────────

// ErrCacheEvictionMissing is the sentinel for "the request supplied
// titles but no Memory service is wired". Maps to 503 Service
// Unavailable in the handler. Distinct from ErrCacheEvictionFailed so
// 1:1 errors.Is → status code mapping is unambiguous.
var ErrCacheEvictionMissing = errors.New("cache-evict: memory service not initialized")

// ErrCacheEvictionFailed is the sentinel for memory-eviction errors
// propagated from Memory.EvictExactOutputs. Maps to 500 Internal
// Server Error in the handler.
var ErrCacheEvictionFailed = errors.New("cache-evict: memory eviction failed")

// ── Use case ────────────────────────────────────────────────────────────────

// memoryCache is the bounded interface needed from the gemmamemory
// cache adapter. Defined locally to avoid importing scripts/adapters
// (which would create a cycle because adapters/test files import usecase).
type memoryCache interface {
	EvictExactOutputs(ctx context.Context, titles []string) (int, error)
	CheckGate(ctx context.Context, req memoryGateRequest) (*memoryGateResult, error)
}

// CacheEvictionUseCase is the orchestrator for /cache/evict.
//
// Nil-safe construction: a nil use case (or a use case with all-nil
// deps) returns ErrCacheEvictionFailed on Run, so the handler can
// panic-free call `.Run(...)` and rely on errors.Is for status mapping.
type CacheEvictionUseCase struct {
	Generator *ollama.Generator
	Memory    memoryCache
	Log       *zap.Logger
}

// NewCacheEvictionUseCase constructs the use case.
func NewCacheEvictionUseCase(
	gen *ollama.Generator,
	mem memoryCache,
	log *zap.Logger,
) *CacheEvictionUseCase {
	return &CacheEvictionUseCase{
		Generator: gen,
		Memory:    mem,
		Log:       log,
	}
}

// Run executes the use case. Returns a typed output; the caller is
// responsible for translating to wire format.
//
// All non-error branches are observable through the structured log
// lines emitted here. The HTTP handler does not add additional log
// noise — every status transition is logged exactly once.
func (u *CacheEvictionUseCase) Run(ctx context.Context, in CacheEvictionInput) (*CacheEvictionOutput, error) {
	if u == nil {
		return nil, fmt.Errorf("%w: use case not constructed", ErrCacheEvictionFailed)
	}

	// ── 1. breaker reset (always — even on empty title list) ─────────
	var resetCount int
	if u.Generator != nil && u.Generator.GetClient() != nil {
		resetCount = u.Generator.GetClient().ResetCircuitBreakers()
		if u.Log != nil {
			u.Log.Info("circuit breakers reset on cache evict",
				zap.Int("models_reset", resetCount))
		}
	}

	// ── 2. fast-path: empty title list ───────────────────────────────
	if len(in.Titles) == 0 {
		return &CacheEvictionOutput{
			CircuitBreakersReset: resetCount,
			DeletedCount:         0,
			EvictedTitles:        nil,
		}, nil
	}

	// ── 3. memory-service missing → 503 ──────────────────────────────
	if u.Memory == nil {
		return nil, ErrCacheEvictionMissing
	}

	// ── 4. evict titles ──────────────────────────────────────────────
	// Defensive copy: the handler advances Titles but a future caller
	// might pass a shared slice; copy so internal state never aliases
	// caller state.
	titles := append([]string(nil), in.Titles...)

	count, err := u.Memory.EvictExactOutputs(ctx, titles)
	if err != nil {
		if u.Log != nil {
			u.Log.Error("failed to evict cache", zap.Error(err))
		}
		// Multi-arg %w: errors.Is walks both wrappers (the sentinel +
		// the original EvictExactOutputs error). errors.Unwrap stops
		// at the first %w pre-Go-1.20; here both chains stay visible.
		return nil, fmt.Errorf("%w: %w", ErrCacheEvictionFailed, err)
	}

	return &CacheEvictionOutput{
		CircuitBreakersReset: resetCount,
		DeletedCount:         int64(count),
		EvictedTitles:        titles,
	}, nil
}

// TrimAndFilterTitles drops empty / whitespace-only entries from the
// raw HTTP body list, returning nil when nothing remains (so the use
// case can use a single `len(in.Titles) == 0` short-circuit).
//
// Exported so the HTTP transport can apply the same normalization the
// use case assumes. Test fixtures can also assert "what the use case
// would actually see" without duplicating the trim logic.
func TrimAndFilterTitles(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

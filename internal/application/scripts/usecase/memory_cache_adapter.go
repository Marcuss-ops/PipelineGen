// Package usecase — memory_cache_adapter.go closes the cross-package
// type mismatch between *adapters.Service and the canonical narrow
// memoryCache interface used by usecase.NewEngine.
//
// Why this file exists (TODO #8 drift-fix PR, June 2026):
//
// The narrow engineCache interface in cache_eviction_usecase.go,
// signature:
//
//	type memoryCache interface {
//	    EvictExactOutputs(ctx context.Context, titles []string) (int, error)
//	    CheckGate(ctx context.Context, req memoryGateRequest) (*memoryGateResult, error)
//	}
//
// uses LOCAL lowercase types (memoryGateRequest, memoryGateResult)
// declared in engine.go. The canonical gemmamemory Service in
// adapters/gemmamemory.go exposes:
//
//	func (s *Service) CheckGate(ctx context.Context, req MemoryGateRequest) (*GateResult, error)
//
// using UPPERCASE types from the adapters package. Go treats these as
// distinct types even when their fields are structurally identical,
// so *adapters.Service does NOT satisfy usecase.memoryCache directly.
// engine.go carries the same observation in a "Phase 1c TODO" comment.
//
// This adapter is the contained seam for that mismatch. It performs a
// single field-by-field translation so the rest of the codebase can
// keep the local-narrow types + canonical-adapters-types carve-out
// without an invasive rename. Future schema drift on either side
// surfaces at the single CheckGate method body below — no other
// caller of the adapter needs to change.
//
// Layering note (AGENTS.md Pattern 0): the wrapper lives here in the
// narrow-interface package, not in adapters/, because the wrappers
// owns the translation from canonical-typed concrete → narrow-typed
// fake. The compile-time assertion at the bottom pins static
// conformance so a future drift breaks the build, not the first
// /api/script/generate invocation.
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
)

// MemoryCacheAdapter wraps *adapters.Service so it satisfies the
// canonical local memoryCache interface. The single field-by-field
// translation lives in CheckGate; EvictExactOutputs is already
// signature-compatible so it delegates directly.
type MemoryCacheAdapter struct {
	svc *adapters.Service
}

// NewMemoryCacheAdapter returns an adapter that exposes the wrapped
// gemmamemory Service through the local memoryCache interface.
//
// A nil svc returns a non-nil adapter whose methods all return zero
// values + nil error — callers should still check e.memorySvc != nil
// before invoking, but the adapter itself is nil-safe to construct so
// test fixtures and partial wiring paths don't panic.
func NewMemoryCacheAdapter(svc *adapters.Service) *MemoryCacheAdapter {
	return &MemoryCacheAdapter{svc: svc}
}

// EvictExactOutputs delegates to the wrapped service. Parameter + return
// signatures already match the local memoryCache interface verbatim so
// no translation is required.
//
// Returns 0 for a nil-wrapped service so callers that forget to nil-check
// get a deterministic no-op (not a panic) — same semantics as the
// underlying adapters.Service stub which currently returns 0 always.
func (a *MemoryCacheAdapter) EvictExactOutputs(ctx context.Context, titles []string) (int, error) {
	if a == nil || a.svc == nil {
		return 0, nil
	}
	return a.svc.EvictExactOutputs(ctx, titles)
}

// CheckGate translates the local memoryGateRequest to the adapters
// package's exported MemoryGateRequest, calls the wrapped Service,
// and converts the resulting GateResult back to the local
// memoryGateResult.
//
// Field mapping is a defensive copy (no pointer aliasing) across the
// 8 MemoryGateRequest fields and 4 GateResult fields. A future schema
// change in either package requires touching this method only.
//
// Nil wrappée return: adapters.Service always returns (*GateResult, nil)
// on cache miss; this adapter keeps the same shape — nil result is
// the cache-miss signal the engine emits as "skip the cache path".
func (a *MemoryCacheAdapter) CheckGate(ctx context.Context, req memoryGateRequest) (*memoryGateResult, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	res, err := a.svc.CheckGate(ctx, adapters.MemoryGateRequest{
		ChannelID:    req.ChannelID,
		Title:        req.Title,
		Prompt:       req.Prompt,
		Language:     req.Language,
		Mode:         req.Mode,
		CacheKey:     req.CacheKey,
		UseMemory:    req.UseMemory,
		ForceRefresh: req.ForceRefresh,
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return &memoryGateResult{
		Hit:       res.Hit,
		Output:    res.Output,
		WordCount: res.WordCount,
		Model:     res.Model,
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): *MemoryCacheAdapter
// statically implements the local memoryCache narrow interface. A
// future drift (signature change on either side) breaks the build at
// this line instead of at the first /api/script/generate invocation.
var _ memoryCache = (*MemoryCacheAdapter)(nil)

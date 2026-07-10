// Package semantic — nop.go: the canonical NOOP implementation of
// `MetadataWriterPort` (P0-#2, July 2026).
//
// Per the user spec: "reintrodurre la capability SOLO tramite una
// porta + implementazione reale (o un noop esplicito)". The nop is
// the EXPLICIT degradation — it logs a disabled marker on
// construction (so operators can grep for the canonical string from
// observability + audit log surfaces) and returns the typed
// `ErrSemanticMetadataWriterDisabled` sentinel on every method call
// (so callers can branch via `errors.Is` for graceful degradation).
//
// The previous `semantic.MetadataWriterPort` fake concrete + the
// `semantic.NewMetadataWriter` constructor are RETIRED. Callers
// either:
//
//   - Receive `nil` (when the composition root elects to skip
//     construction — the production path, since the real
//     implementation is not yet reintroduced), OR
//
//   - Receive `semantic.NewNopMetadataWriter(log)` (when the
//     consumer needs the typed sentinel for graceful degradation
//     — e.g., the soundeffect handler that logs the error and
//     continues, the sfxMeta port adapter that forwards the
//     sentinel to its own caller).
//
// godlike/07 NO-FAKE-AVAILABILITY: this nop is named explicitly,
// logs explicitly, and returns the typed sentinel. There is no
// implicit "did the work" path — every call surfaces the disabled
// condition.
package semantic

import (
	"context"

	"go.uber.org/zap"
)

// NewNopMetadataWriter constructs the canonical nop implementation
// of `MetadataWriterPort`. Logs a single Warn marker on construction
// (so operators can grep for "semantic.MetadataWriterPort" in init
// logs to confirm the no-op is the expected surface, NOT a missing
// wiring) and returns the port interface backed by a private
// `nopWriter` struct that returns
// `ErrSemanticMetadataWriterDisabled` on every method call.
//
// Args:
//
//   - log: the composition-root logger. Nil-safe (falls back to
//     zap.NewNop() so the nop can be used in tests with a noop
//     logger).
//
// The returned value is a `MetadataWriterPort` — callers depend on
// the port, not on the concrete, per AGENTS.md Pattern 0.
func NewNopMetadataWriter(log *zap.Logger) MetadataWriterPort {
	if log == nil {
		log = zap.NewNop()
	}
	log.Warn("semantic.MetadataWriterPort wired as EXPLICIT NOP (P0-#2, July 2026) — real Ollama/Python semantic tagger has not been reintroduced; every GeneratePayload/Write call returns ErrSemanticMetadataWriterDisabled")
	return &nopWriter{}
}

// nopWriter is the private concrete behind the canonical nop. It is
// intentionally zero-cost (no fields) — the nop's only behaviour is
// to return the typed sentinel on every method call. The struct is
// private so callers cannot reach for the concrete and must depend
// on the port instead (AGENTS.md Pattern 0).
type nopWriter struct{}

// GeneratePayload returns (nil, "", ErrSemanticMetadataWriterDisabled)
// per godlike/07 no-fake-availability. The previous shape
// `(*Payload, "", nil)` with a synthetic Payload shell is RETIRED —
// the nop never fabricates a Payload. Callers branch on the returned
// sentinel.
//
// The "string" return value is reserved for future forward-compat
// (was "" in the synthetic-payload shape) and is now ALWAYS "".
func (nopWriter) GeneratePayload(_ context.Context, _ WriteRequest) (*Payload, string, error) {
	return nil, "", ErrSemanticMetadataWriterDisabled
}

// Write returns (nil, ErrSemanticMetadataWriterDisabled) per
// godlike/07. Pre-fix the method returned
// `(&WriteResult{Payload: synthesisedShell, LocalPath: req.LocalPath}, nil)` —
// the LocalPath echo was a particularly insidious fake-availability
// (callers persisted the echo-path as if a real write happened).
// Retired.
func (nopWriter) Write(_ context.Context, _ WriteRequest) (*WriteResult, error) {
	return nil, ErrSemanticMetadataWriterDisabled
}

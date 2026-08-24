// Package enrichment — emitter.go (PR-011C, July 2026).
//
// AssetPublishedEmitter is the canonical Pattern-0 typed port for
// emitting the asset.published v1 outbox event from the
// EnrichmentHandler. The handler builds the v1 envelope (via the
// canonical outbox.AssetPublishedRequestV1 struct) after the
// LLM call + UPDATE succeed, then hands it to this port for
// emission to the outbox_events table.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - AssetPublishedEmitter interface lives ONLY in this file.
//   - StubAssetPublishedEmitter (test/noop concrete) lives ONLY
//     in this file.
//   - The production concrete (outbox-dispatcher-backed) is wired
//     by the composition root in
//     internal/app/build_bundles_stock.go::BuildStockBundle.
//   - The wire-shape of the v1 envelope is the canonical
//     outbox.AssetPublishedRequestV1 (in
//     internal/capabilities/jobs/outbox/asset_published.go).
//
// godlike/07 fail-closed contracts:
//   - EmitAssetPublished returns a typed sentinel (ErrEnrichmentEmitFailed)
//     on transient infrastructure failure (SQLite locked, I/O error).
//     The worker's exponential backoff retries this sentinel up to
//     DefaultMaxRetries=3 before flipping terminal. Idempotency on
//     retry is guaranteed by the outbox_events UNIQUE constraint on
//     event_key — a successful retry on the second attempt is a
//     no-op at the SQLite level (ON CONFLICT DO NOTHING).
//   - Payload validation (empty asset_id, empty destination) is the
//     producer's responsibility (the handler validates BEFORE
//     calling EmitAssetPublished). The port itself does NOT
//     re-validate — that would duplicate the work and risk drift.
//
// godlike/07 minimum-blast-radius: the port is additive — the
// stock pipeline (PR-001..PR-009 chain) does not call this port.
// The composition root wires the emitter ONLY when
// cfg.External.StockEnrichmentEnabled=true (mirroring the existing
// EnrichmentLLMClient gate at build_bundles_stock.go).
package enrichment

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/outbox"
)

// AssetPublishedEmitter is the narrow Pattern-0 typed port for
// emitting asset.published v1 outbox events. godlike/06 SSOT:
// this interface is the SOLE definition of the emit contract for
// the enrichment pass; the composition root wires the production
// concrete (outbox-dispatcher-backed) and tests wire
// StubAssetPublishedEmitter.
//
// Implementations:
//   - StubAssetPublishedEmitter (this file — noop, returns nil).
//   - The future production concrete (composition root wires
//     outbox.Dispatcher.EnqueueAssetPublished — added in PR-011C
//     follow-up if not yet present) or a thin adapter that wraps
//     outboxEventsRepo.Enqueue inside a tx.
//
// Method contract:
//   - ctx propagates to the underlying SQL write; the production
//     concrete respects ctx cancellation per godlike/07
//     nil-tolerance (no goroutine leaks on a cancelled request).
//   - payload is the canonical v1 envelope (handler-validated
//     for required fields BEFORE calling this method).
//   - Returns nil on successful enqueue; ErrEnrichmentEmitFailed
//     on transient failure (retryable); other typed sentinels for
//     terminal failures (e.g. JSON marshal error — should not
//     happen with a valid payload but is guarded for godlike/07
//     no-fake-availability).
type AssetPublishedEmitter interface {
	EmitAssetPublished(ctx context.Context, payload outbox.AssetPublishedRequestV1) error
}

// StubAssetPublishedEmitter is the canonical noop concrete for
// tests + composition-root disabled mode. Returns nil on every
// call. Captures the last-emitted payload in LastPayload for
// hermetic TDD assertions (so tests can verify the handler built
// the correct v1 envelope without standing up the full outbox
// dispatcher + SQLite).
//
// godlike/06 SSOT: StubAssetPublishedEmitter lives ONLY in this
// file. Tests construct one per test case; the composition root
// never wires this stub in production (a wired stub would silently
// drop events — godlike/07 NO-FAKE-AVAILABILITY violation).
type StubAssetPublishedEmitter struct {
	// LastPayload captures the most-recent EmitAssetPublished
	// payload for hermetic TDD assertions. nil until the first
	// emit call. Read-only from the test's perspective.
	LastPayload *outbox.AssetPublishedRequestV1

	// CallCount tracks the number of emit invocations. Tests
	// use this to assert the emit happened exactly once per
	// HandleJob call (no duplicate emits on retry).
	CallCount int
}

// EmitAssetPublished captures the payload + increments the call
// counter + returns nil. The stub NEVER returns an error — tests
// that need to exercise the retry path use a different fake
// (e.g. errorAssetPublishedEmitter in handler_test.go).
//
// godlike/07 nil-tolerance: nil-receiver safe. A nil
// *StubAssetPublishedEmitter can be called via the interface
// without panicking (the method is a value receiver, not a
// pointer receiver).
func (s *StubAssetPublishedEmitter) EmitAssetPublished(ctx context.Context, payload outbox.AssetPublishedRequestV1) error {
	_ = ctx
	if s == nil {
		return nil
	}
	s.LastPayload = &payload
	s.CallCount++
	return nil
}

// Compile-time assertion: *StubAssetPublishedEmitter satisfies
// AssetPublishedEmitter. Catches signature drift at build time
// per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ AssetPublishedEmitter = (*StubAssetPublishedEmitter)(nil)

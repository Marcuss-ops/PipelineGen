// Package enrichment — errors.go (PR-011A, July 2026).
//
// Typed-error sentinels for the stock post-publish RLM/LLM enrichment
// pass. The godlike/07 typed-error contract requires EVERY error
// returned from the EnrichmentHandler to be a typed sentinel so callers
// can probe with errors.Is() / errors.As() and act on the specific
// failure mode (retry vs. terminal vs. re-think).
//
// Sentinel taxonomy (8 sentinels, disjoint semantic classes):
//
//  1. ErrEnrichmentHandlerNotConfigured — composition-time wiring gap.
//     Returned from HandleJob when the handler is constructed without
//     the required dependencies (LLM client, asset repository).
//     godlike/07 fail-closed at composition: this sentinel should be
//     unreachable on a properly-wired production server; production
//     callers surface it as a server-side 500 (operator-dashboard
//     signal to re-examine the composition root).
//
//  2. ErrEnrichmentChunkNotFound — terminal. The chunk_id in the
//     job payload does not match any media_assets row. No retry — the
//     row will not appear from a transient blip. Terminal status flip
//     surfaces on the dashboard as "stale job enqueued".
//
//  3. ErrEnrichmentLLMUnavailable — retryable. The LLM client could
//     not be reached (ollama down, network partition, model not
//     loaded). The worker's exponential backoff retries this
//     sentinel up to DefaultMaxRetries (3) before flipping terminal.
//     This is the canonical "transient infrastructure" signal.
//
//  4. ErrEnrichmentInvalidLLMResponse — terminal after retries. The
//     LLM returned a response that the JSON parser rejected
//     (malformed JSON, missing required field, schema drift). Retry
//     will not help — the LLM needs a different prompt or the
//     response schema needs to evolve. After 3 retries the job
//     flips terminal with this sentinel so the operator can
//     investigate the LLM-side drift.
//
//  5. ErrEnrichmentPersistFailed — terminal. The UPDATE on
//     media_assets.metadata_json failed (SQL error, lock contention,
//     disk full). This is NOT a transient class — a failed UPDATE
//     likely indicates a deeper infrastructure problem; retry would
//     mask it. Terminal on first failure.
//
// All 8 sentinels are package-level `var Err...` declarations so
// callers can probe via errors.Is(err, enrichment.ErrXxx) from any
// package without import-side alias gymnastics. Dual-%w wraps in the
// handler preserve the chain for both errors.Is (sentinel) and
// errors.As (typed envelope) probes (Go 1.20+).
//
// The 8-sentinel taxonomy matches the canonical P0-typed-error
// surface shipped across the project (PR-COMPLETE-WORKER-BROAD-FIX +
// PR-VO-TYPED-PRIMITIVES + PR-ARTLIST-FAKE-AVAILABILITY precedent):
// composition-time + chunk-lookup + LLM-unavailable + LLM-bad-response
// + persist-failed.
package enrichment

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"errors"
	"fmt"
)

// ErrEnrichmentHandlerNotConfigured is returned when the handler is
// constructed without the required dependencies. Composition-time
// sentinel — surfaces as a wiring gap before the worker pool accepts
// the first media.stock_rlm_enrich job.
var ErrEnrichmentHandlerNotConfigured = errors.New("enrichment: handler not configured (missing required dependency)")

// ErrEnrichmentChunkNotFound is returned when the chunk_id in the
// job payload does not match any media_assets row. Terminal sentinel
// — the row will not appear from a transient blip.
var ErrEnrichmentChunkNotFound = errors.New("enrichment: chunk not found in media_assets")

// ErrEnrichmentLLMUnavailable is returned when the LLM client could
// not be reached. Retryable sentinel — the worker's exponential
// backoff retries up to DefaultMaxRetries before flipping terminal.
var ErrEnrichmentLLMUnavailable = errors.New("enrichment: LLM unavailable (ollama down, network partition, or model not loaded)")

// ErrEnrichmentInvalidLLMResponse is returned when the LLM
// returned a response that the JSON parser rejected. Terminal
// after retries — retry will not help; the LLM needs a different
// prompt or the response schema needs to evolve.
var ErrEnrichmentInvalidLLMResponse = errors.New("enrichment: LLM returned an invalid response (malformed JSON, missing required field, or schema drift)")

// ErrEnrichmentPayloadInvalid is returned when the job payload
// itself is malformed (non-JSON, missing required fields, schema
// drift in the producer). Terminal on first failure — the
// producer (e.g. the stock pipeline fan-out in PR-011C) needs
// to fix the enqueue call. Retry would mask the producer-side
// bug.
//
// Distinct from ErrEnrichmentInvalidLLMResponse (which covers
// LLM-side response parse failures and is retryable up to 3 times
// before flipping terminal). The two error classes have
// different recovery strategies:
//   - Payload invalid → terminal, producer must fix enqueue
//   - LLM response invalid → retry, LLM might give a better
//     response next time
var ErrEnrichmentPayloadInvalid = errors.New("enrichment: job payload is malformed (non-JSON, missing required fields, or producer-side schema drift)")

// ErrEnrichmentPersistFailed is returned when the UPDATE on
// media_assets.metadata_json failed. Terminal on first failure —
// retry would mask the underlying infrastructure problem.
var ErrEnrichmentPersistFailed = errors.New("enrichment: failed to persist enriched fields to media_assets")

// ErrEnrichmentEmitFailed is returned when the asset.published v1
// outbox event emit failed. RETRYABLE — the worker pool's
// exponential backoff retries this sentinel up to DefaultMaxRetries
// before flipping terminal. A retry re-emits the same event_key,
// which collapses on the outbox_events UNIQUE constraint via
// ON CONFLICT (event_key) DO NOTHING — so a successful retry on
// the second attempt is a no-op at the SQLite level.
//
// Distinct from ErrEnrichmentPersistFailed (terminal UPDATE
// failure): the UPDATE on media_assets.metadata_json may have
// SUCCEEDED but the subsequent outbox emit may have failed (e.g.
// outbox_events table temporarily locked, SQLite I/O error). The
// UPDATE is idempotent on retry (same EnrichedFields input produces
// byte-identical metadata_json) so re-running the handler is safe;
// the outbox emit is also idempotent on retry (same event_key
// collapses via UNIQUE constraint). Both surfaces are independently
// retryable.
//
// godlike/07 NO-FAKE-AVAILABILITY: this sentinel is REACHABLE on
// transient infrastructure failures. The retry path must NOT
// silently no-op on emit failure — a worker that catches this
// sentinel and returns nil would mask the outbox gap and the asset
// would never be re-upserted to Qdrant via the AssetPublishedHandler
// path.
var ErrEnrichmentEmitFailed = errors.New("enrichment: failed to emit asset.published v1 outbox event (retryable)")

// WrapHandlerNotConfigured wraps the canonical sentinel with a
// specific missing-dependency name. Mirrors the godlike/07
// dual-%w pattern (Go 1.20+) so errors.Is recovers the sentinel
// AND errors.As recovers the underlying diagnostic envelope.
func WrapHandlerNotConfigured(missingDep string) error {
	return fmt.Errorf("%w: missing %s", ErrEnrichmentHandlerNotConfigured, missingDep)
}

// WrapChunkNotFound wraps the canonical sentinel with the
// specific chunk_id that was not found. Operators see the
// failing ID directly in the job_events audit trail.
func WrapChunkNotFound(chunkID string) error {
	return fmt.Errorf("%w: chunk_id=%s", ErrEnrichmentChunkNotFound, chunkID)
}

// WrapLLMUnavailable wraps the canonical retryable sentinel with
// the underlying LLM-client error. Preserves the underlying
// chain via errors.As for diagnostics.
func WrapLLMUnavailable(cause error) error {
	return fmt.Errorf("%w: %v", ErrEnrichmentLLMUnavailable, cause)
}

// WrapInvalidLLMResponse wraps the canonical terminal sentinel
// with the parse failure detail. Operators see the parser
// error directly.
func WrapInvalidLLMResponse(parseErr error) error {
	return fmt.Errorf("%w: %v", ErrEnrichmentInvalidLLMResponse, parseErr)
}

// WrapPayloadInvalid wraps the canonical terminal sentinel
// for malformed-payload failures with the JSON unmarshal
// error detail. The producer (e.g. the stock pipeline fan-out
// in PR-011C) sees the parser error directly so the enqueue
// call site can be fixed.
//
// Distinct from WrapInvalidLLMResponse (LLM-side parse failure).
// The two error classes have different recovery strategies:
//   - WrapPayloadInvalid → terminal, producer must fix enqueue
//   - WrapInvalidLLMResponse → retry, LLM might give a better
//     response next time
func WrapPayloadInvalid(parseErr error) error {
	return fmt.Errorf("%w: %v", ErrEnrichmentPayloadInvalid, parseErr)
}

// WrapPersistFailed wraps the canonical terminal sentinel with
// the SQL update error. Preserves the underlying chain via
// errors.As for diagnostics.
func WrapPersistFailed(cause error) error {
	return fmt.Errorf("%w: %v", ErrEnrichmentPersistFailed, cause)
}

// WrapEmitFailed wraps the canonical retryable sentinel with the
// outbox-emit error. Preserves the underlying chain via errors.As
// for diagnostics. Callers (the worker's exponential backoff) probe
// errors.Is(err, ErrEnrichmentEmitFailed) to decide the retry
// strategy.
func WrapEmitFailed(cause error) error {
	return fmt.Errorf("%w: %v", ErrEnrichmentEmitFailed, cause)
}

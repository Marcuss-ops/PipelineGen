// Package enrichment — outbox_emitter.go (PR-011C follow-up, July 2026).
//
// outboxBackedAssetPublishedEmitter is the production concrete for
// the AssetPublishedEmitter port. It satisfies the canonical port
// declared in emitter.go and persists asset.published v1 envelopes
// to the outbox_events table via the canonical outboxevents.Repository
// (per the codebase's "every code path that mutates authoritative
// data AND triggers an external side-effect MUST route through
// this repository's Enqueue method inside a transaction" invariant
// at internal/platform/sqlite/outboxevents/repository.go:1-22).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - outboxBackedAssetPublishedEmitter lives ONLY in this file.
//   - The AssetPublishedEmitter port (1-method interface) lives
//     ONLY in emitter.go (the canonical port per godlike/06 SSOT).
//   - The v1 envelope wire-shape lives ONLY in
//     jobs.AssetPublishedRequestV1 (canonical SOLE owner).
//   - The event-type constant lives ONLY at
//     outboxevents.EventAssetPublished (canonical SOLE owner).
//   - The aggregate_type value "media_asset" is the codebase's
//     canonical (matches the sibling asset.index.requested +
//     asset.drive.delete_requested emitters per rg-verified
//     usage in dispatcher_index.go + dispatcher_delete.go).
//
// godlike/07 typed-error contract:
//   - json.Marshal error → ErrEnrichmentEmitFailed (retryable;
//     a marshal error means the payload is structurally bad
//     — retrying won't help, but the worker's exponential
//     backoff will flip terminal after 3 attempts per the
//     existing retry contract).
//   - db.BeginTx error → ErrEnrichmentEmitFailed (retryable).
//   - repo.Enqueue error → ErrEnrichmentEmitFailed (retryable;
//     on retry the same idempotency_key collapses to a no-op
//     at the SQLite level via ON CONFLICT DO NOTHING).
//   - tx.Commit error → ErrEnrichmentEmitFailed (retryable).
//   - EnqueueResult.Inserted=false (ON CONFLICT suppressed) →
//     log Info (idempotency contract honored) + return nil.
//     This is the EXPECTED behavior on retry: same
//     (chunkID, fileHash, version) triple → same idempotency_key
//     → ON CONFLICT suppresses the second insert → no fake event.
//
// godlike/07 minimum-blast-radius: the producer-side open
// happens via db.BeginTx (NOT inherited from a caller-provided tx).
// The EnrichmentHandler has no parent tx (it's a worker
// handler that runs the LLM call + UPDATE + emit as 3
// independent operations). The emit is its own logical
// unit, so a fresh tx is the canonical scope.
//
// godlike/07 NO-FAKE-AVAILABILITY: every code path that
// successfully returns nil has either committed a new
// outbox_events row OR observed that the ON CONFLICT
// contract suppressed the insert (idempotency honored).
// No silent-success path exists — a successful emit
// ALWAYS corresponds to a row in outbox_events (new or
// pre-existing with the same event_key).
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// aggregateTypeMediaAsset is the canonical aggregate_type value
// for asset.published v1 events. Mirrors the existing
// asset.index.requested + asset.drive.delete_requested emitters
// (rg-verified in dispatcher_index.go + dispatcher_delete.go).
// godlike/06 SSOT: this constant lives ONLY in this file; future
// event types MUST declare their own aggregate_type constant
// alongside the new struct (NOT redefine this one).
const aggregateTypeMediaAsset = "media_asset"

// outboxBackedAssetPublishedEmitter is the production concrete
// for the AssetPublishedEmitter port. Persists asset.published
// v1 envelopes to the outbox_events table via a fresh SQL tx.
//
// The struct holds a *sql.DB (NOT a *outboxevents.Repository)
// so the per-call tx is local to EmitAssetPublished. The
// Repository is constructed per-call (it's a stateless wrapper
// around db.ExecContext + db.QueryRowContext) — caching the
// Repository is unnecessary.
//
// godlike/07 fail-closed: the constructor returns an error
// (NOT a nil adapter) when db is nil. Callers MUST propagate
// the error per the existing pattern in NewEnrichmentHandler.
type outboxBackedAssetPublishedEmitter struct {
	// db is the canonical *sql.DB handle. The composition root
	// injects the same DB handle the broker uses (canonical
	// SSOT for "which DB does the system read from").
	db *sql.DB

	// log is the canonical zap logger. godlike/07 nil-tolerance:
	// nil-logger falls back to zap.NewNop() (defense-in-depth).
	log *zap.Logger
}

// NewOutboxBackedAssetPublishedEmitter constructs the production
// outbox-backed emitter with fail-closed nil-deps gate per
// godlike/07 typed-error contract. Returns (nil,
// ErrEnrichmentHandlerNotConfigured) when db is nil.
func NewOutboxBackedAssetPublishedEmitter(db *sql.DB, log *zap.Logger) (*outboxBackedAssetPublishedEmitter, error) {
	if db == nil {
		return nil, WrapHandlerNotConfigured("db")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &outboxBackedAssetPublishedEmitter{db: db, log: log}, nil
}

// EmitAssetPublished persists the v1 envelope to the outbox_events
// table inside a fresh SQL transaction. The full lifecycle:
//
//  1. Marshal payload to JSON (WrapEmitFailed on failure).
//  2. db.BeginTx (WrapEmitFailed on failure).
//  3. repo.Enqueue inside the tx (WrapEmitFailed on failure).
//  4. tx.Commit (WrapEmitFailed on failure; the tx is rolled
//     back implicitly when Commit returns an error).
//  5. Log Info on success with the canonical payload summary
//     (event_id + idempotency_key + aggregate_id) for audit.
//
// godlike/07 NO-FAKE-AVAILABILITY: a successful return means
// the row IS in outbox_events (either newly inserted or
// ON CONFLICT-suppressed by an existing row with the same
// event_key). The idempotency_key field is the canonical
// event_key (mirrors the canonical AssetPublishedRequestV1
// field per godlike/06 SSOT).
//
// godlike/07 nil-tolerance: nil-receiver safe. A nil
// *outboxBackedAssetPublishedEmitter returns the composition-time
// sentinel (mirrors the stub's nil-tolerance discipline).
func (e *outboxBackedAssetPublishedEmitter) EmitAssetPublished(ctx context.Context, payload jobs.AssetPublishedRequestV1) error {
	if e == nil || e.db == nil {
		return WrapHandlerNotConfigured("emitter")
	}

	// Step 1: marshal the v1 envelope to JSON. The handler
	// already validated the payload (per godlike/06 SSOT —
	// payload validation is the handler's responsibility, NOT
	// the port's), so marshal errors are structurally-impossible
	// in practice. The WrapEmitFailed wrap is the defensive
	// fail-closed contract for godlike/07 NO-FAKE-AVAILABILITY.
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return WrapEmitFailed(fmt.Errorf("outboxBackedAssetPublishedEmitter.EmitAssetPublished: marshal v1 envelope: %w", err))
	}

	// Step 2: open a fresh tx. The EnrichmentHandler has no
	// parent tx (it's a worker handler that runs the LLM call
	// + UPDATE + emit as 3 independent operations), so the
	// emit is its own logical unit. A fresh tx is the canonical
	// scope per godlike/07 minimum-blast-radius.
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return WrapEmitFailed(fmt.Errorf("outboxBackedAssetPublishedEmitter.EmitAssetPublished: BeginTx: %w", err))
	}
	// Defer Rollback as a safety net. Rollback is a no-op if
	// Commit succeeded; otherwise it cleans up the tx state
	// on the rare panic or early-return path.
	defer func() {
		_ = tx.Rollback() // best-effort; Commit success makes this a no-op
	}()

	// Step 3: enqueue inside the tx. The canonical Repository
	// uses ON CONFLICT(event_key) DO NOTHING for idempotency
	// (per outboxevents/repository.go:122 docstring). On
	// retry (same payload.IdempotencyKey) the second insert
	// is suppressed; EnqueueResult.Inserted=false signals
	// this and ExistingStatus carries the existing row's
	// status so the producer can decide whether to retry
	// with a new event_key or move on.
	//
	// For the enrichment pass, same (chunkID, fileHash,
	// version) → same idempotency_key → Inserted=false on
	// retry is the EXPECTED behavior. We log Info and
	// return nil (idempotency contract honored; no error).
	repo := outboxevents.NewRepository(e.db)
	result, err := repo.Enqueue(ctx, tx, outboxevents.EventAssetPublished, payload.AssetID, aggregateTypeMediaAsset, string(payloadJSON), payload.IdempotencyKey)
	if err != nil {
		return WrapEmitFailed(fmt.Errorf("outboxBackedAssetPublishedEmitter.EmitAssetPublished: Enqueue(asset_id=%s, event_key=%s): %w", payload.AssetID, payload.IdempotencyKey, err))
	}

	// Step 4: commit. If commit fails, the deferred Rollback
	// runs (no-op for an already-rolled-back tx; the row
	// will NOT be in outbox_events).
	if err := tx.Commit(); err != nil {
		return WrapEmitFailed(fmt.Errorf("outboxBackedAssetPublishedEmitter.EmitAssetPublished: Commit: %w", err))
	}

	// Step 5: log the canonical payload summary for audit.
	// On Inserted=false (idempotency contract honored) we log
	// the existing row's status so operators can distinguish
	// "fresh event" from "retry-no-op".
	if e.log != nil {
		fields := []zap.Field{
			zap.String("event_type", outboxevents.EventAssetPublished),
			zap.String("event_id", payload.EventID),
			zap.String("asset_id", payload.AssetID),
			zap.String("idempotency_key", payload.IdempotencyKey),
			zap.String("destination", payload.Destination),
			zap.Bool("inserted", result.Inserted),
		}
		if !result.Inserted {
			fields = append(fields, zap.String("existing_status", result.ExistingStatus))
			e.log.Info("outboxBackedAssetPublishedEmitter.EmitAssetPublished: ON CONFLICT suppressed (idempotency contract honored; no new row)", fields...)
		} else {
			e.log.Info("outboxBackedAssetPublishedEmitter.EmitAssetPublished: row inserted", fields...)
		}
	}

	return nil
}

// Compile-time assertion: *outboxBackedAssetPublishedEmitter
// satisfies AssetPublishedEmitter. Catches signature drift at
// compile time per AGENTS.md Pattern 0 / godlike/06 SSOT.
var _ AssetPublishedEmitter = (*outboxBackedAssetPublishedEmitter)(nil)

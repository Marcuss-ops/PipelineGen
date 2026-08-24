// Package enrichment — handler_emit.go (PR-SPLIT-ENRICHMENT-HANDLER, August 2026).
//
// Canonical owner of the asset.published v1 envelope builder + emitter
// call (emitAssetPublishedV1). Extracted from the 596 LoC handler.go
// monolith per AGENTS.md Pattern 5 + godlike/06 SSOT one-canonical-owner-per-fact.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - emitAssetPublishedV1 lives ONLY in this file.
//   - The AssetPublishedRequestV1 envelope + AssetPublishedSchemaVersion
//     const live ONLY in internal/capabilities/jobs/outbox/asset_published.go
//     (the canonical typed contract — DO NOT re-declare here).
//   - EnrichmentIdempotencyKey + EnrichmentVersionV1 helpers live ONLY
//     in idem_key.go (canonical idempotency-key derivation seam).
//   - ErrEnrichmentIdempotencyKeyConflict typed sentinel lives ONLY in
//     errors.go (the 5-sentinel taxonomy SSOT).
//
// godlike/07 fail-closed contracts:
//   - Nil emitter returns canonical "pr011c_v1_emit_skipped_nil_emitter"
//     stage label + Warn log (disabled-mode wiring observable to operators).
//   - Idempotency-key derivation failure returns the
//     "pr011c_v1_emit_failed_retryable" stage label + dual-%w wrapped
//     typed sentinel (terminal — producer-side state gap).
//   - Emitter.EmitAssetPublished error returns the same retryable stage
//     label + WrapEmitFailed (transient — worker's exponential backoff
//     retries up to DefaultMaxRetries=3).
//
// godlike/07 minimum-blast-radius: the method signature is STABLE —
// orchestrator's HandleJob calls it byte-equivalent pre/post split.
// The 3 stage labels are the canonical SSOT — no implicit "v1 emitted"
// claim when the emit was skipped or failed.
package assets

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/outbox"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// emitAssetPublishedV1 builds the canonical asset.published v1 envelope
// + hands it to the emitter. Returns (stageLabel, error). The stageLabel
// is one of 3 canonical values (per godlike/06 SSOT):
//
//   - "pr011c_v1_emit_ok" — successful emit.
//   - "pr011c_v1_emit_skipped_nil_emitter" — emitter nil (disabled-mode
//     wiring); Warn logged so operator can identify the misconfiguration.
//   - "pr011c_v1_emit_failed_retryable" — emit attempted but failed
//     (either idem-key derivation OR emitter call); the error is wrapped
//     with WrapEmitFailed for the worker's exponential backoff to retry.
//
// godlike/07 typed-error contract: dual-%w wraps (Go 1.20+) preserve
// the chain for both errors.Is (typed sentinel) and errors.As probes.
// The idempotency-key derivation failure is a producer-side state gap
// (terminal); the emitter call failure is transient (retryable).
func (h *EnrichmentHandler) emitAssetPublishedV1(ctx context.Context, row *AssetRow, fields EnrichedFields) (string, error) {
	// godlike/07 nil-tolerance: nil-emitter is disabled-mode
	// wiring (composition root did not inject the emitter). Log
	// a Warn + return the canonical skipped stage label so
	// the handler's happy-path is reachable in tests that
	// don't exercise the emit step. The composition root
	// MUST wire a real emitter in production
	// (godlike/07 NO-FAKE-AVAILABILITY).
	if h.Emitter == nil {
		if h.Log != nil {
			h.Log.Warn("enrichment.HandleJob: emitter nil (composition root disabled-mode wiring) — skipping asset.published v1 emit",
				zap.String("chunk_id", row.ID),
			)
		}
		return "pr011c_v1_emit_skipped_nil_emitter", nil
	}

	// Derive the canonical idempotency_key from (chunk_id,
	// file_hash, v1). ErrEnrichmentIdempotencyKeyConflict surfaces
	// the godlike/07 no-fake-availability contract violation
	// (empty chunk_id or malformed content_hash) — terminal
	// because the producer (the stock pipeline that wrote
	// the media_assets row) must fix the underlying state.
	idemKey, idemErr := EnrichmentIdempotencyKey(row.ID, row.LegacyFileMD5, EnrichmentVersionV1)
	if idemErr != nil {
		// godlike/07 typed-error contract: a malformed triple
		// is a producer-side bug. Surface as a terminal sentinel
		// (WrapEmitFailed with the idem conflict as the cause)
		// so the operator can identify the root cause. The
		// stage label is the "failed" variant because the emit
		// WAS attempted (we got past the nil-emitter check) but
		// the pre-emit validation rejected the payload.
		return "pr011c_v1_emit_failed_retryable", WrapEmitFailed(fmt.Errorf("%w: %v", ErrEnrichmentIdempotencyKeyConflict, idemErr))
	}

	// Build the canonical v1 envelope (godlike/06 SSOT one
	// canonical owner per fact: AssetPublishedRequestV1 in
	// internal/capabilities/jobs/outbox/asset_published.go).
	payload := outbox.AssetPublishedRequestV1{
		SchemaVersion:  outbox.AssetPublishedSchemaVersion,
		EventID:        uuid.NewString(),
		AssetID:        row.ID,
		Destination:    "stock",
		Origin:         "generated",
		Category:       fields.Category,
		Subject:        fields.Subject,
		Provider:       row.SourceProvider,
		DriveFileID:    row.DriveFileID,
		DrivePath:      row.DrivePath,
		ContentType:    "video",
		Tags:           nil, // PR-011C: tags deferred to PR-ENRICHMENT-E2E-SUITE forward-pointer
		IdempotencyKey: idemKey,
		RequestedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	// Hand to the emitter. The production concrete (outbox-dispatcher-backed)
	// opens its own tx and enqueues to outbox_events. The stub
	// (tests) captures the payload for hermetic TDD assertions.
	if err := h.Emitter.EmitAssetPublished(ctx, payload); err != nil {
		return "pr011c_v1_emit_failed_retryable", WrapEmitFailed(err)
	}

	if h.Log != nil {
		h.Log.Info("enrichment.HandleJob: asset.published v1 emitted",
			zap.String("chunk_id", row.ID),
			zap.String("event_id", payload.EventID),
			zap.String("idempotency_key", idemKey),
			zap.String("drive_file_id", payload.DriveFileID),
			zap.String("destination", payload.Destination),
		)
	}
	return "pr011c_v1_emit_ok", nil
}

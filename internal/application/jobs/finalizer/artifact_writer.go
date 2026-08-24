// Package finalizer — artifact_writer.go (PR-GODOBJ-5-FINALIZER split)
//
// Hosts the artifact-write + outbox-event-write surface for the
// JobFinalizer. Two methods move from the pre-split monolithic
// job_finalizer.go into this file:
//
//   - writeArtifacts — extracts step 7 of the orchestrator's
//     transactional pipeline: delegate each PublishedArtifact to
//     f.assetTx.FinalizeAsset(ctx, domainTx, a) → collect the
//     returned ArtifactRef + OutboxEvent(s).
//
//   - writeOutboxEvents — step 8: enqueue every OutboxEvent
//     (request-side + asset-side) via f.outbox.Enqueue inside
//     the open transaction. Idempotent via the outbox's UNIQUE
//     (event_key) constraint (the outbox-side ON CONFLICT
//     collapses retry-side duplicates — that's a separate
//     surface in outboxevents.Repository, NOT this file).
//
// Both methods operate INSIDE the open transaction the orchestrator
// started at step 2 (CompleteWithArtifacts). They MUST NOT call
// tx.Commit / tx.Rollback — transaction lifecycle is owned by the
// orchestrator alone.
//
// godlike/06 SSOT: this file is the canonical owner of "how do
// artifacts + outbox events get persisted for job X?". Callers
// MUST route through these methods — never re-implement the
// loop inline.
package finalizer

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// writeArtifacts delegates each request artifact to AssetFinalizerTx
// inside the open transaction. Returns the collected ArtifactRef
// slice (for the orchestrator's FinalizationResult) and the
// outbox-event slice emitted by AssetFinalizerTx (for the orchestrator's
// outbox-enqueue step).
//
// godlike/07 typed-error contract: each per-artifact failure is
// wrapped with a positional hint (`artifact[%d] (%s)`) so the log
// stream attributes the failure to the right artifact without
// forcing the operator to dereference the index in the request.
func (f *Finalizer) writeArtifacts(
	ctx context.Context,
	domainTx finalization.Transaction,
	artifacts []finalization.PublishedArtifact,
) ([]finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	// ── PR-FINALIZER-METRICS (July 2026) ──────────────────────────
	// Increment FinalizerWriteArtifactsIterTotal per iteration.
	// outcome=ok per successful FinalizeAsset; outcome=err when
	// the iteration returned a non-nil error (writeArtifacts then
	// early-returns with the wrapped error, propagating to the
	// parent FinalizerCompleteArtifactsTotal{outcome="err"} bump).
	// Per-iteration cardinality (NOT per-call) so an operator can
	// alert on `ratio(err/ok) > X` regardless of batch size.
	refs := make([]finalization.ArtifactRef, 0, len(artifacts))
	artifactEvents := make([]finalization.OutboxEvent, 0, len(artifacts))
	if f.log != nil {
		f.log.Info("finalizer: writeArtifacts enter",
			zap.Int("input_count", len(artifacts)))
	}
	for i, a := range artifacts {
		ref, events, err := f.assetTx.FinalizeAsset(ctx, domainTx, a)
		if err != nil {
			metrics.FinalizerWriteArtifactsIterTotal.WithLabelValues("err").Inc()
			if f.log != nil {
				f.log.Warn("finalizer: writeArtifacts iteration failed",
					zap.Int("i", i),
					zap.String("artifact_id", a.ArtifactID),
					zap.Error(err))
			}
			return nil, nil, fmt.Errorf("finalizer: artifact[%d] (%s): %w", i, a.ArtifactID, err)
		}
		metrics.FinalizerWriteArtifactsIterTotal.WithLabelValues("ok").Inc()
		refs = append(refs, ref)
		artifactEvents = append(artifactEvents, events...)
		if f.log != nil {
			f.log.Debug("finalizer: writeArtifacts iter ok",
				zap.Int("i", i),
				zap.String("artifact_id", a.ArtifactID),
				zap.Int("refs_so_far", len(refs)),
				zap.Int("events_so_far", len(artifactEvents)))
		}
	}
	if f.log != nil {
		f.log.Info("finalizer: writeArtifacts exit",
			zap.Int("refs_returned", len(refs)),
			zap.Int("events_returned", len(artifactEvents)))
	}
	return refs, artifactEvents, nil
}

// ── Outbox events ───────────────────────────────────────────────────

// writeOutboxEvents enqueues all outbox events inside the transaction
// using the outboxevents.Repository.
//
// The empty-payload fallback (`{}` instead of `""` / `null`) keeps the
// outbox's payload_json NOT NULL constraint stable across capability
// payloads that legitimately emit no data — without the fallback, those
// events would INSERT with an empty string and fail audit-side
// JSON-parse on the consumer side.
func (f *Finalizer) writeOutboxEvents(
	ctx context.Context,
	tx *sql.Tx,
	events []finalization.OutboxEvent,
) error {
	for i, evt := range events {
		payloadJSON := string(evt.Payload)
		if payloadJSON == "" || payloadJSON == "null" {
			payloadJSON = "{}"
		}
		_, err := f.outbox.Enqueue(ctx, tx,
			evt.EventType,
			evt.AggregateID,
			"", // aggregate_type — not required for asset events
			payloadJSON,
			evt.EventKey,
		)
		if err != nil {
			return fmt.Errorf("finalizer: outbox event[%d] (%s): %w", i, evt.EventType, err)
		}
	}
	return nil
}

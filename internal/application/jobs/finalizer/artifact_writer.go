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
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
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
	// ── PR-FINALIZER-DIAG-COUNTER (July 2026) ──────────────────────
	// Diagnostic wrap around the artifact-write loop. Goal: surface
	// which gate is filtering out rows (e.g. 7 manifest entries -> 0
	// media_assets rows). Ordering of stderr prints:
	//   "writeArtifacts input_count=N"  — at function entry
	//   "writeArtifacts artifact[i] (id) ok refs_so_far=R events_so_far=E"
	//                                    — per successful iter
	//   "writeArtifacts artifact[i] (id) FAILED err=..." — per failed iter
	//   "writeArtifacts EXITS ok refs=R events=E"  — at function exit
	// This lets operators see manifest-vs-persisted delta without
	// having to dereference stack frames: every filter gate that
	// drops below the input_count surfaces as a missing-iter line.
	fmt.Fprintf(os.Stderr, "[finalizer][debug] writeArtifacts input_count=%d\n", len(artifacts))
	if f.log != nil {
		f.log.Info("finalizer: writeArtifacts enter",
			zap.Int("input_count", len(artifacts)))
	}
	refs := make([]finalization.ArtifactRef, 0, len(artifacts))
	artifactEvents := make([]finalization.OutboxEvent, 0, len(artifacts))
	for i, a := range artifacts {
		ref, events, err := f.assetTx.FinalizeAsset(ctx, domainTx, a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[finalizer][debug] writeArtifacts artifact[%d] (%s) FAILED err=%v\n",
				i, a.ArtifactID, err)
			if f.log != nil {
				f.log.Warn("finalizer: writeArtifacts iteration failed",
					zap.Int("i", i),
					zap.String("artifact_id", a.ArtifactID),
					zap.Error(err))
			}
			return nil, nil, fmt.Errorf("finalizer: artifact[%d] (%s): %w", i, a.ArtifactID, err)
		}
		refs = append(refs, ref)
		artifactEvents = append(artifactEvents, events...)
		fmt.Fprintf(os.Stderr, "[finalizer][debug] writeArtifacts artifact[%d] (%s) ok refs_so_far=%d events_so_far=%d\n",
			i, a.ArtifactID, len(refs), len(artifactEvents))
	}
	fmt.Fprintf(os.Stderr, "[finalizer][debug] writeArtifacts EXITS ok refs_returned=%d events_returned=%d\n",
		len(refs), len(artifactEvents))
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

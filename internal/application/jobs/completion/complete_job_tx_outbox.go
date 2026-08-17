// Package completion — complete_job_tx_outbox.go: outbox event emission + codec.
//
// 2026-07-06 (Phase 4 decomposition): extracted from complete_job_service.go
// per the god-object decomposition plan. Pure code-motion, zero behavior
// changes.
//
// emitOutboxEvents fans out canonical outbox events for the completed job.
// Each event has a unique (jobID, attempt, event_kind) idempotency key
// so retries collapse to one row in the outbox_events table.
//
// codecIDForPayload pins the canonical codec discriminator for the
// result payload.
package completion

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// emitOutboxEvents fans out canonical outbox events for the
// completed job. Each event has a unique (jobID, attempt,
// event_kind) idempotency key so retries collapse to one row in
// the outbox_events table.
func (s *Service) emitOutboxEvents(ctx context.Context, tx TxContext, req *remote.CompleteJobRequest, artifactIDs []string) error {
	// One summary JOB_COMPLETED event. The event_key is the canonical
	// `job.completed:<jobID>` shared with SQLiteStore.Complete/Fail and the
	// JobFinalizer so a cross-path re-completion of the same job dedups to
	// one outbox row (ON CONFLICT(event_key) DO NOTHING).
	if err := tx.InsertOutboxEnvelope(ctx, OutboxEnvelope{
		IdempotencyKey: outboxevents.JobCompletedEventKey(req.JobID),
		EventKind:      outboxevents.EventJobCompleted,
		Payload:        req.Result,
	}); err != nil {
		return fmt.Errorf("insert job.completed envelope: %w", err)
	}
	// One ARTIFACT_UPLOADED event per artifact.
	for _, a := range req.Artifacts.Artifacts {
		evKind := "artifact." + a.Kind + ".uploaded"
		auKey := remote.CompleteJobIdempotencyKey(req.JobID, req.Attempt, evKind+":"+a.ID)
		if err := tx.InsertOutboxEnvelope(ctx, OutboxEnvelope{
			IdempotencyKey: auKey,
			EventKind:      evKind,
			Payload:        []byte(a.ID),
		}); err != nil {
			return fmt.Errorf("insert %s envelope: %w", evKind, err)
		}
	}
	return nil
}

// codecIDForPayload pins the canonical codec discriminator for
// the result payload. The canonical ResultCodec enum is owned by
// the C2 compiled-registry surface; this helper returns the
// stable ID for json payloads today (the only codec installed
// per the C1/C2 spec).
func codecIDForPayload(payload []byte) string {
	if len(payload) == 0 {
		return "empty"
	}
	return "json.v1"
}

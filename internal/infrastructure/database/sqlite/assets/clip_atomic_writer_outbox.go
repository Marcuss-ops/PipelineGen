// Package assets — clip_atomic_writer_outbox.go: outbox_events
// tx-bound INSERT helper + post-commit typed-error contract
// (BLOCKER #4 closure). Split from the orchestrator
// clip_atomic_writer.go for per-table responsibility
// (clip_atomic_writer split, July 2026).
//
// godlike/06 SSOT (single tx invariant): this file accepts *sql.Tx
// as a parameter and NEVER opens its own transaction. The actual
// outbox INSERT happens inside `outboxevents.Repository.Enqueue`
// which is keyed to the SAME *sql.Tx — the tx is owned by the
// orchestrator (clip_atomic_writer.go). Adding a BeginTx call here
// would shatter the atomic surface.
//
// godlike/06 SSOT (typed-error contract): when an existing terminal
// row (dead_letter or superseded) suppresses the INSERT, the writer
// returns `youtubeports.ErrOutboxTerminalConflict`. Pre-closure
// the writer logged a warning and returned nil (silent success →
// "processed" with no index event). Post-closure (audit
// 2026-07-03 BLOCKER #4) we surface the typed sentinel so the use
// case can render "processed_but_index_blocked".
//
// godlike/06 SSOT (helper extraction): the BLOCKER #4 post-commit
// check was duplicated across both entry points (CommitClipAndIndexEvent
// step 6, CommitClipTextAndIndexEvent step 8). This helper extracts
// the logic so the orchestrator calls `checkOutboxTerminalAfterCommit`
// once per tx-bound write; we deduplicate the typed-error wrapping
// while preserving the canonical log fields (clip_id, event_key,
// existing_event_id, existing_status).
package assets

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// enqueueClipIndexEventInTx is a thin wrapper around
// outboxevents.Repository.Enqueue that binds the call to the
// orchestrator's *sql.Tx. Extracted from the inline body of both
// entry points so the outbox write surface is centralised.
//
// godlike/06 SSOT: this is the SOLE place where the writer's outbox
// INSERT happens. The outbox events table requires the producer-
// state mutation (UPSERT media_assets) AND the outbox INSERT to
// commit in the SAME tx — both entry points hand the orchestrator's
// `*sql.Tx` here.
//
// Returned EnqueueResult carries the existing-status when
// ON CONFLICT(event_key) DO NOTHING fires so the caller (via
// checkOutboxTerminalAfterCommit) can surface the BLOCKER #4 typed
// error without re-querying.
func enqueueClipIndexEventInTx(
	ctx context.Context,
	box *outboxevents.Repository,
	tx *sql.Tx,
	eventType string,
	aggregateID string,
	assetID string,
	payloadJSON string,
	eventKey string,
) (*outboxevents.EnqueueResult, error) {
	if tx == nil {
		return nil, fmt.Errorf("enqueueClipIndexEventInTx: tx is nil")
	}
	if box == nil {
		return nil, fmt.Errorf("enqueueClipIndexEventInTx: outboxevents.Repository is nil")
	}
	return box.Enqueue(
		ctx,
		tx,
		eventType,
		aggregateID,
		"media_asset",
		payloadJSON,
		eventKey,
	)
}

// isTerminalOutboxStatus reports whether an outbox row's status is
// terminal — useful for deciding whether a fresh INSERT was squelched
// by an already-completed/failed event, vs the more benign case
// where the same key was already in pending/processing.
//
// godlike/06 SSOT (typed-error contract): only terminal statuses
// (dead_letter / superseded) trigger the BLOCKER #4 typed-error
// return. Pending / processing collisions are benign (the same
// event is already scheduled) and the caller can treat the write as
// successful after Commit succeeds.
func isTerminalOutboxStatus(status string) bool {
	return status == "dead_letter" || status == outboxevents.SupersedeStatus
}

// checkOutboxTerminalAfterCommit inspects the EnqueueResult AFTER
// the orchestrator has called tx.Commit(). If the INSERT was
// squelched by an existing terminal row, this helper returns the
// BLOCKER #4 typed-error sentinel (`youtubeports.ErrOutboxTerminalConflict`)
// so the orchestrator propagates it verbatim. The helper also
// emits the canonical operator-facing log line (clip_id, event_key,
// existing_event_id, existing_status) when a logger is provided.
//
// godlike/06 SSOT (helper extraction): this was previously inlined
// in both entry points with identical bodies. Extracting it here
// removes the duplicate error-wrapping code without affecting the
// typed error contract — `errors.Is(err, youtubeports.ErrOutboxTerminalConflict)`
// continues to work identically for both call sites.
//
// godlike/10 (log-shape-preserved): the Warn log fields are kept
// identical to the original inline version (no additive fields
// introduced) so dashboard queries keyed on (clip_id, event_key,
// existing_event_id, existing_status) continue to match.
func checkOutboxTerminalAfterCommit(
	log *zap.Logger,
	enqResult *outboxevents.EnqueueResult,
	clipID string,
	eventKey string,
) error {
	if enqResult.Inserted {
		return nil
	}
	if !isTerminalOutboxStatus(enqResult.ExistingStatus) {
		return nil
	}
	err := fmt.Errorf("%w: clip %q event_key=%q suppressed by existing %q row (event_id=%d)",
		youtubeports.ErrOutboxTerminalConflict, clipID, eventKey,
		enqResult.ExistingStatus, enqResult.EventID)
	if log != nil {
		log.Warn("ClipAtomicWriterAdapter: returning ErrOutboxTerminalConflict (BLOCKER #4 closure)",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.Int64("existing_event_id", enqResult.EventID),
			zap.String("existing_status", enqResult.ExistingStatus),
			zap.Error(err))
	}
	return err
}

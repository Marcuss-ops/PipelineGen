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
package imagesregistry

import (
	"fmt"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

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

// checkOutboxTerminalAfterCommit inspects the outbox enqueue result
// AFTER the orchestrator has called tx.Commit(). If the event was
// suppressed by an existing terminal row, this helper returns the
// BLOCKER #4 typed-error sentinel (`youtubeports.ErrOutboxTerminalConflict`)
// so the orchestrator propagates it verbatim. The helper also
// emits the canonical operator-facing log line (clip_id, event_key,
// existing_status) when a logger is provided.
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
// existing_status) continue to match.
func checkOutboxTerminalAfterCommit(
	log *zap.Logger,
	inserted bool,
	clipID string,
	eventKey string,
	existingStatus string,
) error {
	if inserted {
		return nil
	}
	// The event was not inserted. Only surface the BLOCKER #4
	// typed sentinel when the existing row is in a terminal state
	// (dead_letter or superseded). Pending / processing collisions
	// are benign — the same event is already scheduled.
	if !isTerminalOutboxStatus(existingStatus) {
		return nil
	}
	err := fmt.Errorf("%w: clip %q event_key=%q suppressed by existing terminal row (status=%q)",
		youtubeports.ErrOutboxTerminalConflict, clipID, eventKey, existingStatus)
	if log != nil {
		log.Warn("ClipAtomicWriterAdapter: returning ErrOutboxTerminalConflict (BLOCKER #4 closure)",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("existing_status", existingStatus),
			zap.Error(err))
	}
	return err
}

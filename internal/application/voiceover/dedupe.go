// Package voiceover — dedupe.go (PR-VO-B3 post-upload dedupe gate, June 2026).
//
// Owns the typed PR-VO-B3 dedupe verdict (DedupeDecision) and the pure
// DecideDedupe projection. The gate's SQL body is the canonical
// persistence.Repository.CountByDriveFileIDTx port method (implemented by the
// composition-root adapter). The pre-P1-2 inline applyDedupeByDriveFileID
// helper that consumed raw *sql.DB / *sql.Tx was retired once that port
// landed, so this file carries zero database/sql imports (iobinder
// PR-REFACTOR-P0-IO-BINDER-SQLITE contract).
package voiceover

// DedupeDecision is the typed PR-VO-B3 dedupe verdict. The finalizeStage
// gate acts on the decision (Continue/Reuse/Conflict) rather than logging
// and continuing into the PR-VO-A2 atomic-swap.
//
// Semantics:
//
//   - DedupeContinue : 0 matches → gate is a no-op, fall through
//     to the canonical DELETE+INSERT+Outbox+Commit sequence.
//   - DedupeReuse    : 1 match   → REUSE the matched (canonical)
//     row; skip INSERT (matched row already represents the
//     DriveFileID). item.ID is reassigned to matchedID so the
//     response references the canonical record.
//   - DedupeConflict : >1 matches → AMBIGUOUS — fail-closed per
//     godlike/07's no-fake-availability policy. Surface
//     FailureDedupeAmbiguous + deferred tx.Rollback().
type DedupeDecision string

const (
	DedupeContinue DedupeDecision = "continue"
	DedupeReuse    DedupeDecision = "reuse"
	DedupeConflict DedupeDecision = "conflict"
)

// String implements fmt.Stringer so the decision logs cleanly via
// zap.String without manual conversion (operators grep `decision=`
// in pipeline audit logs).
func (d DedupeDecision) String() string { return string(d) }

// DecideDedupe projects CountByDriveFileIDTx's `count` signal into
// the typed DedupeDecision. Pure function (no I/O) so the boundary
// semantics are pinned by table-driven tests in dedupe_test.go.
//
// Contract: count==0 → Continue, count==1 → Reuse, count>1 → Conflict.
// Negative (defensive) collapses to Continue.
func DecideDedupe(count int) DedupeDecision {
	switch {
	case count == 1:
		return DedupeReuse
	case count > 1:
		return DedupeConflict
	default:
		return DedupeContinue
	}
}

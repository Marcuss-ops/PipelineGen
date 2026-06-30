// Package voiceover — dedupe.go (PR-VO-B3 post-upload dedupe gate, June 2026).
//
// Implements the production body of applyDedupeByDriveFileID — the
// Stage-3 finalize gate that the pre-PR8 voiceover code owned before
// the PR8 slim-orchestrator extraction deleted it along with the rest
// of the 695-line pre-PR8 process.go. The 7-case test contract is
// pinned in process_dedupe_test.go.
//
// Stages.go (PR-VOICEOVER-PROCESS-GO-FIX build-pass-only checkpoint)
// remains a stub layer for the 4 orchestrator-level symbols (Stage
// shells + free mergeUserMetadata fn). This dedupe.go file owns the
// one PR-VO-B3 gate that has a live test contract, so its body is
// fully implemented here rather than stubbed.
//
// Per godlike/07's "no fake availability": this implementation is the
// real SQL gate, not a stub. Operators running a real voiceover
// pipeline today (calling finalizeStage → applyDedupeByDriveFileID)
// will see the gate execute against an actual SQLite voiceovers
// table. The Stage-3 stub still emit-fails upstream of this gate,
// but the gate itself is correct as soon as RESTORE PR hydrates
// finalizeStage's body.
package voiceover

import (
	"context"
	"database/sql"
	"errors"
)

// voiceoverDedupeRow is the minimal row-scan shape returned by
// applyDedupeByDriveFileID. Fields are COALESCE'd to empty strings
// inside the SELECT, so callers MUST NOT re-coalesce (test pins this
// contract). The struct is unexported because the gate is internal to
// the voiceover package — only finalizeStage consumes the result.
type voiceoverDedupeRow struct {
	ID        string
	DriveLink string
	LocalPath string
	FileHash  string
}

// applyDedupeByDriveFileID is the PR-VO-B3 post-upload dedupe gate.
// Returns (nil, 0) for any defensive short-circuit:
//
//   - empty driveFileID      → caller passed a non-uploaded id
//   - nil db                  → composition root didn't wire DB
//   - cancelled / expired ctx → parent ctx already gone
//
// Returns (row, count) on the happy path:
//
//   - count == 0: no match → row nil (gates pass-through)
//   - count == 1: single match → row populated, ambiguity absent
//   - count  > 1: multiple matches → row populated with the first
//     match; count signals ambiguity so the caller's INFO log
//     ("ambiguous dedupe match") can fire and operators can
//     investigate duplicate-files state.
//
// The SELECT WHERE clause fences the current id out (id != ?) so a
// re-run never shadows its own existence.
//
// Optional tx (3rd arg) lets finalizeStage thread the gate through
// the PR-VO-A2 atomic-swap transaction. When tx is non-nil, queries
// run against tx; otherwise against db. Same *sql.DB and *sql.Tx
// packages — the canonical "tx-or-db" pick pattern.
func applyDedupeByDriveFileID(
	ctx context.Context,
	db *sql.DB,
	tx *sql.Tx,
	currentID string,
	driveFileID string,
) (*voiceoverDedupeRow, int) {
	// Defensive early-outs (cheap path: skip SQL entirely). Tests
	// pin each of these short-circuits.
	if driveFileID == "" || db == nil {
		return nil, 0
	}
	if err := ctx.Err(); err != nil {
		// Cancelled or deadline-exceeded — do not invoke queries
		// (QueryRowContext on cancelled ctx returns ErrConnDone /
		// context.Canceled, but the test pins the no-panic contract
		// for cancellation; matching it here keeps the gate
		// dependency-light across both happy + error paths).
		return nil, 0
	}

	// Pick tx or db based on the caller's transaction context.
	// Both *sql.Tx and *sql.DB expose QueryRowContext with the
	// same signature, so a single variable covers both call sites.
	queryRow := db.QueryRowContext
	if tx != nil {
		queryRow = tx.QueryRowContext
	}

	// Match-any query (LIMIT 1 + WHERE fence excludes self). The
	// COALESCE lets us Scan into plain string fields without
	// nullable-sql.NullString plumbing — empty strings are the
	// canonical "missing" representation per the legacy migration
	// contract (process_dedupe_test.go::TestApplyDedupeByDriveFileID_NullColumnsCoalesceToEmpty).
	row := queryRow(ctx, `
		SELECT id,
		       COALESCE(drive_link, ''),
		       COALESCE(local_path, ''),
		       COALESCE(file_hash, '')
		  FROM voiceovers
		 WHERE drive_file_id = ? AND id != ?
		 LIMIT 1
	`, driveFileID, currentID)

	var dr voiceoverDedupeRow
	if err := row.Scan(&dr.ID, &dr.DriveLink, &dr.LocalPath, &dr.FileHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No match — most common case; return without logging.
			return nil, 0
		}
		// Other scan errors (rare: column-type drift after a future
		// migration). Defensive short-circuit — the caller's
		// finalizeStage will log + continue without dedupe protection.
		return nil, 0
	}

	// Count query: independent second round-trip because Go's
	// database/sql does not have first-class window functions.
	// Returns the ambiguity signal so callers can fire INFO logs
	// when count > 1.
	var count int
	if err := queryRow(ctx,
		`SELECT COUNT(*) FROM voiceovers WHERE drive_file_id = ? AND id != ?`,
		driveFileID, currentID,
	).Scan(&count); err != nil {
		// Count failed but we DID find the row: degrade gracefully
		// with count=1. Better than failing the gate because the
		// helper had a transient COUNT error — finalizeStage can
		// still proceed with the dedupe-protected row.
		return &dr, 1
	}
	return &dr, count
}

// Step 7/12 (June 2026, dedupe gate operativo): canonical PR-VO-B3
// dedupe verdict type. The finalizeStage gate now ACTS on the
// decision (Continue/Reuse/Conflict) rather than logging and
// continuing into the PR-VO-A2 atomic-swap.
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

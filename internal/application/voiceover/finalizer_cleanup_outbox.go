// Package voiceover — finalizer_cleanup_outbox.go
// (PR-VO-FINALIZER-STEP6-EXTRACT, P0 #3 in VO-DECOMPOSITION-2026-07-04, deadline 2026-08-01).
//
// finalizer_cleanup_outbox.go is the SINGLE canonical owner of Step 6
// (Outbox — voiceover.cleanup.requested, REQUIRED) per godlike/06
// SSOT (one owner per fact). It is the thin-mechanical extraction of
// the Step 6 block from finalizer.go, with the P0.7 Step 10/12
// atomic swap-and-cleanup triplo logic (OldDriveFileID + OldLocalPath
// + OldCleanedPath) preserved VERBATIM.
//
// Mechanical scope (godlike/07 minimal-blast-radius, pure code-motion):
//   - Moved:  the requiredStepCleanupOutbox constant (wire-stable step
//     identity; lives ONLY here after this PR).
//   - Extracted:  the Step 6 execution block (~28 LoC of switch +
//     oldLocalPaths build + EnqueueCleanupEvent call + execution-marker
//     selection) into the executeCleanupOutboxStep helper.
//   - Preserved:  the fail-fast Outbox nil check stays in Finalize()
//     (godlike/07 ZERO LEGACY: wiring is a precondition for BOTH
//     Step 5 index outbox AND Step 6 cleanup outbox; the caller MUST
//     pass a non-nil outbox).
//   - Preserved:  the P0.7 tripla cleanup contract EXACTLY as it was
//     in the pre-extraction finalizer.go. The tripla logic is
//     forward-pointer for the P0.7 Step 10/12 owner; this PR MUST
//     NOT refactor the ShouldSwap / oldLocalPaths / EnqueueCleanupEvent
//     logic without coordinating with the P0.7 ticket.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - this file owns the step ID "cleanup_outbox" (the constant
//     requiredStepCleanupOutbox lives ONLY here).
//   - finalizer.go owns the cross-cutting state markers
//     requiredStateExecuted / requiredStateGuarded (used by Steps
//     4, 5, 6, and any future required step).
//   - finalizer.go owns the formatRequiredState helper (cross-cutting
//     pure string helper shared by all required steps).
//   - The executeCleanupOutboxStep function references those
//     cross-cutting facts by direct call (same package, no import
//     surface; no SSOT violation).
package voiceover

import (
	"context"
	"database/sql"
	"fmt"
)

// requiredStepCleanupOutbox is the wire-stable step identifier for
// Step 6 (Outbox — voiceover.cleanup.requested). It is the SINGLE
// canonical owner of the "cleanup_outbox" step identity per
// godlike/06 SSOT. Pre-PR-VO-FINALIZER-STEP6-EXTRACT this constant
// lived in finalizer.go; it is moved here so the cleanup outbox
// step is owned by the file that implements it.
//
// Audit P0 #2 (July 2026): the string value "cleanup_outbox" is
// byte-equivalent with the pre-P0 #2 SkippedSteps value, so
// log-grep anchors + operator alerting rules keyed on this substring
// continue to fire. Renaming is HOW a downstream audit pins
// unannounced drift.
const requiredStepCleanupOutbox = "cleanup_outbox"

// executeCleanupOutboxStep is the SINGLE canonical implementation of
// Step 6 (Outbox — voiceover.cleanup.requested, REQUIRED) per
// godlike/06 SSOT. It handles the Replace-Mode cleanup triplo
// (OldDriveFileID + OldLocalPath + OldCleanedPath) per the P0.7
// Step 10/12 atomic swap-and-cleanup contract.
//
// Returns:
//   - string: the pre-formatted RequiredSteps execution marker
//     (one of "cleanup_outbox: executed" /
//     "cleanup_outbox: guarded (ShouldSwap=false)" /
//     "cleanup_outbox: guarded (no prior artefacts)").
//   - error: a non-nil error if the EnqueueCleanupEvent call failed;
//     the wrapped %w error chain is preserved for errors.Is traversal
//     by the caller (finalizeStage maps the error to
//     BatchItem.Status=StatusFailed).
//
// Pre-conditions (caller-contract, NOT runtime-checked):
//   - outbox MUST be non-nil (fail-fast at Finalize() entry in
//     finalizer.go; godlike/07 ZERO LEGACY). Passing nil is a
//     programming error; the EnqueueCleanupEvent call would panic.
//
// godlike/07 minimal-blast-radius: the P0.7 tripla cleanup logic
// (ShouldSwap / oldLocalPaths build / EnqueueCleanupEvent call /
// execution-marker switch) is preserved VERBATIM from the pre-PR
// finalizer.go. NO refactoring of the tripla logic is in scope.
func executeCleanupOutboxStep(
	ctx context.Context,
	tx *sql.Tx,
	outbox TxOutboxEnqueuer,
	cmd *FinalizeCommand,
) (string, error) {
	cleanupExecuted := false
	if cmd.ShouldSwap {
		var oldLocalPaths []string
		if cmd.OldLocalPath != "" {
			oldLocalPaths = append(oldLocalPaths, cmd.OldLocalPath)
		}
		if cmd.OldCleanedPath != "" && cmd.OldCleanedPath != cmd.OldLocalPath {
			oldLocalPaths = append(oldLocalPaths, cmd.OldCleanedPath)
		}
		if cmd.OldDriveFileID != "" || len(oldLocalPaths) > 0 {
			if err := outbox.EnqueueCleanupEvent(ctx, tx,
				cmd.ID,
				cmd.OldDriveFileID,
				cmd.DriveFileID,
				oldLocalPaths,
			); err != nil {
				return "", fmt.Errorf("voiceoverFinalizer: EnqueueCleanupEvent: %w", err)
			}
			cleanupExecuted = true
		}
	}
	switch {
	case cleanupExecuted:
		return formatRequiredState(requiredStepCleanupOutbox, requiredStateExecuted), nil
	case !cmd.ShouldSwap:
		return formatRequiredState(requiredStepCleanupOutbox, requiredStateGuarded, "ShouldSwap=false"), nil
	default:
		return formatRequiredState(requiredStepCleanupOutbox, requiredStateGuarded, "no prior artefacts"), nil
	}
}

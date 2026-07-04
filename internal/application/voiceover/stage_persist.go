// Package voiceover — stage_persist.go (PR-VO-STAGES-SPLIT, P0 #2 in
// VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-01).
//
// Stage 4 of the 5-stage voiceover pipeline: in-tx atomic writes
// via s.finalizer.Finalize. This is a forward-pointer per the
// thinker's godlike/07 minimal-blast-radius recommendation — the
// batch pipeline (process.go) does NOT call persistStage directly
// in the EXPAND phase. The canonical in-tx persist logic lives in
// finalizer.go (per P0.4 Fase 3a, July 2026) and is invoked from
// finalizeStage.
//
// Mechanical extraction from the pre-split finalizeStage body
// (the in-tx portion: BeginTx + finalizer.Finalize). No behavior
// change in EXPAND. The Service method form is intentional: the
// Service struct already owns voiceoverRepo + finalizer fields,
// so the wiring surface is stable. A future BACKFILL wave will
// split finalizeStage into persistStage (this method) + a slim
// commit/verify wrapper.
//
// Compile-time lock: process_voiceover_item.go reads the same
// FinalizeCommand / FinalizeResult / VoiceoverFinalizer types
// via the finalizer package — preserved verbatim.
package voiceover

import (
	"context"
	"database/sql"
	"fmt"
)

// persistStage is the forward-pointer for Stage 4 of the
// 5-stage pipeline (in-tx atomic writes). It wraps
// s.finalizer.Finalize which owns the 6-step atomic commit
// sequence (dedupe → delete → insert → media_assets projection
// → index outbox → cleanup outbox) per P0.4 Fase 3a, July 2026.
//
// In the current EXPAND phase, the batch pipeline (process.go)
// does NOT call persistStage directly — finalizeStage handles
// both persist (BeginTx + Finalize) and finalize (Commit +
// post-commit verification). This forward-pointer exists to
// document the canonical persist seam and to provide a stable
// API for the BACKFILL phase (which will split persist from
// finalize).
//
// godlike/07 honest-limitation: this method is NOT wired in
// process.go. The current finalizeStage handles both persist
// and finalize. A future wave will wire persistStage into
// process.go and split finalizeStage into commit + verify.
//
// Signature contract:
//   - Caller owns the *sql.Tx (opens it before calling, commits
//     it after, rolls back on error).
//   - Caller builds the FinalizeCommand from the per-item state.
//   - persistStage returns the FinalizeResult (nil on error) so
//     the caller can inspect Reused/CompletionState/RequiredSteps.
func (s *Service) persistStage(
	ctx context.Context,
	tx *sql.Tx,
	cmd *FinalizeCommand,
) (*FinalizeResult, error) {
	if s.finalizer == nil {
		return nil, fmt.Errorf("%s: finalizer not wired (composition root — P0.4 Fase 3a requires VoiceoverFinalizer)", restoreIdent)
	}
	return s.finalizer.Finalize(ctx, tx, cmd)
}

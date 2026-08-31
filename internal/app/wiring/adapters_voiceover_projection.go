// Package app — voiceover LifecycleProjectionUpserter +
// VoiceoverPostCommitVerifier adapters (PR-VO-ADAPTERS-SPLIT,
// July 2026).
//
// Capability cluster: FINALIZATION sidecars. Both adapters feed the
// voiceover finalizer's 6-step atomic commit sequence:
//
//  4. media_assets projection (UpsertVoiceoverProjectionTx, LifecycleProjectionUpserter)
//  6. NEW post-commit verification (Verify, VoiceoverPostCommitVerifier)
//
// Note: this file imports database/sql for the *sql.Tx parameter
// type that the canonical port signatures require (see
// internal/capabilities/voiceover/ports.go::UpsertVoiceoverProjectionTx
// and Verify). The actual SQL work happens in
// Service.UpsertVoiceoverProjectionTx (P0.4 Fase 3a) and
// the bare QueryRowContext calls in voiceoverPostCommitVerifierAdapter.
// Future PR-VO-ADAPTERS-TYPED-PORT (deadline TBD, forward-pointer)
// will abstract the *sql.Tx parameter into a typed envelope so the
// import collapses.
//
// Fail-closed: nil svc / nil db panic at construction (fail-fast per
// AGENTS.md WireUp pattern).
package wiring

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
)

// ─────────────────────────────────────────────────────────────────────
// LifecycleProjectionUpserter adapter (P0.4 Fase 3a, July 2026).
//
// Bridges *Service → voiceover.LifecycleProjectionUpserter.
// The two VoiceoverProjectionInput types (voiceover.VoiceoverProjectionInput
// and VoiceoverProjectionInput) have identical field sets but
// are separate types by design (domain separation — godlike/06 §one-
// owner-per-fact). The adapter translates between them so the
// voiceover.Finalizer stays free of any lifecycle package import.
// ─────────────────────────────────────────────────────────────────────

type voiceoverProjectionAdapter struct {
	svc *lifecycle.Service
}

func newVoiceoverProjectionAdapter(svc *lifecycle.Service) *voiceoverProjectionAdapter {
	if svc == nil {
		panic("app.adapters_voiceover_use_case: newVoiceoverProjectionAdapter: svc is required (*Service)")
	}
	return &voiceoverProjectionAdapter{svc: svc}
}

func (a *voiceoverProjectionAdapter) UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, in *voiceover.VoiceoverProjectionInput) error {
	return a.svc.UpsertVoiceoverProjectionTx(ctx, tx, &lifecycle.VoiceoverProjectionInput{
		ID:            in.ID,
		Source:        in.Source,
		Name:          in.Name,
		Filename:      in.Filename,
		FolderID:      in.FolderID,
		FolderPath:    in.FolderPath,
		MediaType:     in.MediaType,
		LocalPath:     in.LocalPath,
		DriveFileID:   in.DriveFileID,
		DriveLink:     in.DriveLink,
		DownloadLink:  in.DownloadLink,
		LegacyFileMD5: in.LegacyFileMD5,
		// PR-VO-TYPED-PRIMITIVES (July 2026): typed Language is
		// converted to the raw string for the lifecycle package's
		// wire shape (infrastructure layer stays un-typed per the
		// audit scope discipline).
		Language: string(in.Language),
		Status:   in.Status,
		Metadata: in.Metadata,
	})
}

var _ voiceover.LifecycleProjectionUpserter = (*voiceoverProjectionAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// PostCommitVerifier adapter (P0.4 Fase 4a, July 2026).
//
// Bridges *sql.DB → voiceover.VoiceoverPostCommitVerifier.Verify.
// Runs two SELECT queries outside any tx (post-commit) to confirm
// both the voiceovers row and the media_assets projection exist.
// Returns nil when both rows are present; returns a descriptive
// error when either is missing.
// ─────────────────────────────────────────────────────────────────────

type voiceoverPostCommitVerifierAdapter struct {
	db *sql.DB
}

func newVoiceoverPostCommitVerifierAdapter(db *sql.DB) *voiceoverPostCommitVerifierAdapter {
	if db == nil {
		panic("app.adapters_voiceover_use_case: newVoiceoverPostCommitVerifierAdapter: db is required (*sql.DB)")
	}
	return &voiceoverPostCommitVerifierAdapter{db: db}
}

func (a *voiceoverPostCommitVerifierAdapter) Verify(ctx context.Context, voiceoverID string) error {
	// Check voiceovers row.
	var voStatus string
	err := a.db.QueryRowContext(ctx,
		`SELECT status FROM voiceovers WHERE id = ?`, voiceoverID,
	).Scan(&voStatus)
	if err != nil {
		if err == sql.ErrNoRows {
			// Audit P0.5 (July 2026): severe divergence — the canonical
			// voiceovers row itself is missing after the tx committed.
			// Wrap with voiceover.ErrReconciliationRequired so
			// finalizeStage can react via errors.Is and surface
			// CompletionState=StateReconciliationRequired on
			// FinalizeResult (godlike/07 honest signal; godlike/06
			// typed-port contract).
			return fmt.Errorf("post-commit verification: voiceovers row missing for id=%q: %w", voiceoverID, voiceover.ErrReconciliationRequired)
		}
		return fmt.Errorf("post-commit verification: voiceovers SELECT error for id=%q: %w", voiceoverID, err)
	}

	// Check media_assets projection.
	var mediaSource string
	err = a.db.QueryRowContext(ctx,
		`SELECT source FROM media_assets WHERE id = ? AND source = 'voiceover'`, voiceoverID,
	).Scan(&mediaSource)
	if err != nil {
		if err == sql.ErrNoRows {
			// Warn-level divergence: the canonical voiceovers row IS
			// present (verified above) but the secondary media_assets
			// projection is missing. Bare error (not wrapping
			// ErrReconciliationRequired) so finalizeStage maps this to
			// CompletionState=StateCompletedUnverified (audit P0.5).
			return fmt.Errorf("post-commit verification: media_assets projection missing for id=%q (source='voiceover')", voiceoverID)
		}
		return fmt.Errorf("post-commit verification: media_assets SELECT error for id=%q: %w", voiceoverID, err)
	}

	return nil
}

var _ voiceover.VoiceoverPostCommitVerifier = (*voiceoverPostCommitVerifierAdapter)(nil)

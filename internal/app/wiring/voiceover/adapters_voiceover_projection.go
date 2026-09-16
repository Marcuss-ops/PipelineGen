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
// Service.UpsertVoiceoverProjectionTx (P0.4 Fase 3a) — which is now a
// fail-closed stub on the retired legacy branch — and in the
// operational `voiceovers` read in VoiceoverPostCommitVerifierAdapter.
// The media_assets half of that verification goes through
// VoiceoverProjectionChecker (MEDIA-SSOT P2-9 Phase 2) instead of raw SQL.
// Future PR-VO-ADAPTERS-TYPED-PORT (deadline TBD, forward-pointer)
// will abstract the *sql.Tx parameter into a typed envelope so the
// import collapses.
//
// Fail-closed: nil svc / nil db panic at construction (fail-fast per
// AGENTS.md WireUp pattern).
package voiceover

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
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

type VoiceoverProjectionAdapter struct {
	svc *lifecycle.Service
}

func NewVoiceoverProjectionAdapter(svc *lifecycle.Service) *VoiceoverProjectionAdapter {
	if svc == nil {
		panic("app.adapters_voiceover_use_case: NewVoiceoverProjectionAdapter: svc is required (*Service)")
	}
	return &VoiceoverProjectionAdapter{svc: svc}
}

func (a *VoiceoverProjectionAdapter) UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, in *voiceover.VoiceoverProjectionInput) error {
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

var _ voiceover.LifecycleProjectionUpserter = (*VoiceoverProjectionAdapter)(nil)

// ─────────────────────────────────────────────────────────────
// PostCommitVerifier adapter (P0.4 Fase 4a, July 2026).
//
// Bridges the operational handle + a narrow media port →
// voiceover.VoiceoverPostCommitVerifier.Verify. Runs two SELECTs
// outside any tx (post-commit) to confirm both the voiceovers row
// and the media_assets projection exist.
//
// MEDIA-SSOT P2-9 Phase 2: the check spans TWO tables on TWO
// engines — `voiceovers` (operational) and `media_assets`
// (PostgreSQL media SSOT) — so it now takes TWO handles instead of
// one. The previous single-handle form read media_assets on the
// operational store, which holds no committed media rows, so the
// verifier would have reported a MISSING projection for every asset
// the canonical writer had just committed. Splitting the reads is
// the general shape for a two-engine verification: neither engine is
// chosen for the other's table.
// ─────────────────────────────────────────────────────────────

// VoiceoverProjectionChecker is the narrow media-SSOT read the verifier needs:
// whether the canonical voiceover projection of an asset exists. Declaring it
// here keeps this adapter from naming an engine for the media half;
// pgmedia.MediaVoiceoverProjectionChecker implements it and the composition root
// supplies it.
type VoiceoverProjectionChecker interface {
	VoiceoverProjectionExists(ctx context.Context, assetID string) (bool, error)
}

type VoiceoverPostCommitVerifierAdapter struct {
	db *sql.DB
	// media is the media-SSOT half of the verification. nil means the media
	// plane is closed; Verify then reports the projection as unverifiable rather
	// than silently passing, because both possible outcomes of a failed check map
	// to the SAME severity (StateCompletedUnverified), and passing would not.
	media VoiceoverProjectionChecker
}

func NewVoiceoverPostCommitVerifierAdapter(db *sql.DB, media VoiceoverProjectionChecker) *VoiceoverPostCommitVerifierAdapter {
	if db == nil {
		panic("app.adapters_voiceover_use_case: NewVoiceoverPostCommitVerifierAdapter: db is required (*sql.DB)")
	}
	if media == nil {
		panic("app.adapters_voiceover_use_case: NewVoiceoverPostCommitVerifierAdapter: media is required (VoiceoverProjectionChecker; media_assets is PostgreSQL-owned)")
	}
	return &VoiceoverPostCommitVerifierAdapter{db: db, media: media}
}

func (a *VoiceoverPostCommitVerifierAdapter) Verify(ctx context.Context, voiceoverID string) error {
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

	// Check media_assets projection — on the MEDIA SSOT, not on a.db.
	//
	// The retired single-handle form returned a bare error in BOTH the missing-row
	// and the query-error case, so this keeps that shape exactly: the two cases
	// stay distinguishable in the message but share the severity, which
	// finalizeStage maps to CompletionState=StateCompletedUnverified (audit P0.5).
	// Notably, a nil media port must NOT pass the check: an unverifiable
	// projection is not a verified one.
	if a.media == nil {
		return fmt.Errorf("post-commit verification: no media-SSOT projection checker wired for id=%q (media plane closed)", voiceoverID)
	}
	exists, err := a.media.VoiceoverProjectionExists(ctx, voiceoverID)
	if err != nil {
		return fmt.Errorf("post-commit verification: media_assets lookup error for id=%q: %w", voiceoverID, err)
	}
	if !exists {
		// Warn-level divergence: the canonical voiceovers row IS present (verified
		// above) but the secondary media_assets projection is missing. Bare error
		// (not wrapping ErrReconciliationRequired) so finalizeStage maps this to
		// CompletionState=StateCompletedUnverified (audit P0.5).
		return fmt.Errorf("post-commit verification: media_assets projection missing for id=%q (source='voiceover')", voiceoverID)
	}

	return nil
}

var _ voiceover.VoiceoverPostCommitVerifier = (*VoiceoverPostCommitVerifierAdapter)(nil)

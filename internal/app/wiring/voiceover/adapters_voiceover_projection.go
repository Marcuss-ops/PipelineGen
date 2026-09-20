// Package app — voiceover LifecycleProjectionUpserter adapter
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Capability cluster: FINALIZATION sidecars. This adapter feeds the
// voiceover finalizer's 6-step atomic commit sequence at step 4:
//
//  4. media_assets projection (UpsertVoiceoverProjectionTx, LifecycleProjectionUpserter)
//
// VoiceoverPostCommitVerifierAdapter (step 6, P0.4 Fase 4a) was DELETED here on
// 2026-09-20. Its Verify had no caller — not in production, not in tests — and
// the question "wire it at the composition root or delete it" resolves to
// delete on evidence: the port it implemented is consumed by exactly one field
// (voiceover.Service.postCommitVerifier), and voiceover.Service itself has NO
// construction site anywhere in the tree (voiceover.VoiceoverDeps is built only
// in service_test.go). The composition root builds the per-item use case
// (ProcessVoiceoverItemUseCase) instead, whose finalize deps carry only
// Finalizer. So there is nothing to wire the verifier into without first
// resurrecting the retired batch Service, and a verifier that can never run is
// worse than none: finalizer.go's own table records that an unwired verifier
// yields "" for the verification state, which omitempty then hides — the audit
// P0.5 divergence signal was silently absent. Music: wiring this back requires
// deciding to bring up the batch Service again AND confirming the live per-item
// path writes the media_assets projection the verifier checks; until both hold,
// the delete is the honest state.
//
// The media-SSOT half (pgmedia.MediaVoiceoverProjectionChecker) was NOT deleted
// with the adapter: it is a tested, engine-correct media_assets read surface in
// its own right, and removing it would have reached into the postgres/media
// package for no deadcode gain. VoiceoverPostCommitVerifier (the capability
// port) and the nullable Service field also stay; both are inert, and the port
// is what a future re-wire would implement again.
//
// Note: this file imports database/sql for the *sql.Tx parameter
// type that the canonical port signature requires (see
// internal/capabilities/voiceover/ports.go::UpsertVoiceoverProjectionTx).
// The actual SQL work happens in
// Service.UpsertVoiceoverProjectionTx (P0.4 Fase 3a) — which is now a
// fail-closed stub on the retired legacy branch.
//
// Fail-closed: nil svc panics at construction (fail-fast per
// AGENTS.md WireUp pattern).
package voiceover

import (
	"context"
	"database/sql"

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

// The PostCommitVerifier adapter section lived here until 2026-09-20; see the
// package header for why it was deleted and what has to be true before it can
// be wired back.

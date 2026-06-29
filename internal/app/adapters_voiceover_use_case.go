// Package app — voiceover use case adapters (P0.1, June 2026).
//
// Bridges production concretes under internal/infrastructure/* to the
// 7 canonical narrow ports declared in
// internal/application/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *lifecycle
// imports.
//
// Adapters populate:
//
//	TTSProvider          ← *audioasset.Processor
//	AudioPostProcessor   ← pkg-level ffmpeg.RemoveSilence closure
//	AssetLifecycle       ← *lifecycle.Service (ProcessAsset adapter)
//	VoiceoverRepository  ← direct DB adapter (tx.ExecContext for
//	                        InsertTx / DeleteByIDTx; PreReadByID stub;
//	                        column schema mirrors the canonical
//	                        VoiceoversRepository.Upsert)
//	DestinationResolver  ← *asset.Resolver (forward Group + StyleGroup,
//	                        mirror StyleGroup verbatim back)
//
// Two remaining ports (TransactionalOutbox, DB) are passed directly
// from composition:
//
//	*outbox.Dispatcher         struct-satisfies voiceover.TxOutboxEnqueuer
//	                             (= voiceover.TransactionalOutbox) — the
//	                             legacy Service already relies on this
//	                             structural conformance (see
//	                             internal/infrastructure/database/sqlite/
//	                             outbox/repository.go:418).
//	*sql.DB                    injection direct, no adapter.
//
// Compile-time assertions follow the canonical structural-conformance
// convention: each adapter struct on the right side declares
// `var _ voiceover.<Port> = (*<AdapterStruct>)(nil)` so drift between
// the adapter signature and the port contract surfaces as a compile
// error here, NOT at the use case Execute call site.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────
// TTSProvider adapter.
//
// Bridges *audioasset.Processor → voiceover.TTSProvider. The use case
// only ever supplies well-formed inputs (no path-traversal payloads
// past cmd.Validate), so the lower-level AudioInput fields EscapeHook
// semantics — UseStdin defaults to false. AudioResult carries
// LocalPath + CleanedPath + Voice + FileHash which map 1-a-1 to
// TTSOutput.
// ─────────────────────────────────────────────────────────────────────

type useCaseTTSAdapter struct {
	proc *audioasset.Processor
}

func newUseCaseTTSAdapter(proc *audioasset.Processor) *useCaseTTSAdapter {
	if proc == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseTTSAdapter: proc is required (*audioasset.Processor)")
	}
	return &useCaseTTSAdapter{proc: proc}
}

func (a *useCaseTTSAdapter) Synthesize(ctx context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	res, err := a.proc.Generate(ctx, &audioasset.AudioInput{
		Text:          in.Text,
		Language:      in.Language,
		Voice:         in.Voice,
		Filename:      in.Filename,
		OutputDir:     in.OutputDir,
		RemoveSilence: in.RemoveSilence,
		// UseStdin defaults to false: the use case path delivers
		// bounded, validated text inputs (POST /generate path →
		// command.Validate path-traversal rejection before field
		// access, mirrors TestGenerateBatch_RejectsPathTraversalPayload).
	})
	if err != nil {
		return voiceover.TTSOutput{}, err
	}
	return voiceover.TTSOutput{
		LocalPath:   res.LocalPath,
		CleanedPath: res.CleanedPath,
		Voice:       res.Voice,
		FileHash:    res.FileHash,
	}, nil
}

var _ voiceover.TTSProvider = (*useCaseTTSAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// AudioPostProcessor adapter.
//
// Implements voiceover.AudioPostProcessor.Process by wrapping the
// package-level ffmpeg.RemoveSilence closure. The cleaned-path
// convention is deterministic: <OutputDir>/cleaned_<Filename> so
// filename uniqueness rules (P1.3) keep the call surface predictable.
// Nil-safe at the use case boundary (only invoked when
// cmd.RemoveSilence == true).
// ─────────────────────────────────────────────────────────────────────

type useCaseAudioAdapter struct {
	log *zap.Logger
}

func newUseCaseAudioAdapter(log *zap.Logger) *useCaseAudioAdapter {
	return &useCaseAudioAdapter{log: log}
}

func (a *useCaseAudioAdapter) Process(ctx context.Context, in voiceover.AudioPostInput) (voiceover.AudioPostOutput, error) {
	if in.LocalPath == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty LocalPath (use case passed a missing local path)")
	}
	if in.OutputDir == "" || in.Filename == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty OutputDir/Filename (use case contract violation)")
	}
	// clean file goes to <OutputDir>/cleaned_<basename>; matches the
	// canonical convention used by audioasset.Processor (processor.go:103).
	cleaned := in.OutputDir + "/cleaned_" + in.Filename
	if a.log != nil {
		a.log.Info("voiceover.audio_post: stripping silence",
			zap.String("input", in.LocalPath),
			zap.String("output_dir", in.OutputDir),
			zap.String("filename", in.Filename))
	}
	if err := ffmpeg.RemoveSilence(ctx, "", in.LocalPath, cleaned); err != nil {
		if a.log != nil {
			a.log.Warn("voiceover.audio_post: RemoveSilence failed (caller decides whether to fail-fast)",
				zap.String("input", in.LocalPath),
				zap.String("output", cleaned),
				zap.Error(err))
		}
		return voiceover.AudioPostOutput{}, err
	}
	return voiceover.AudioPostOutput{CleanedPath: cleaned}, nil
}

var _ voiceover.AudioPostProcessor = (*useCaseAudioAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// AssetLifecycle adapter.
//
// Bridges *lifecycle.Service.ProcessAsset → voiceover.AssetLifecycle.Upload.
// ProcessAsset is the canonical "dedupe + Drive upload + persist" entry
// (PR-VO-B1 hardened: fails-fast on upload failure rather than the
// legacy log-warn best-effort). The use case already manages identity
// via the atomic swap tx (InsertTx + DeleteByIDTx inside
// ProcessOneLanguage), so the adapter disables dedupe (VerifyDB=false)
// and asserts the use case contract by enabling RequireLocal +
// RequireHash + RequireDrive on every call. FinalizeResult.OK==false
// surfaces as an error so the use case Execute path bubbles up the
// failure rather than silently dropping it.
// ─────────────────────────────────────────────────────────────────────

type useCaseLifecycleAdapter struct {
	svc *lifecycle.Service
}

func newUseCaseLifecycleAdapter(svc *lifecycle.Service) *useCaseLifecycleAdapter {
	if svc == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseLifecycleAdapter: svc is required (*lifecycle.Service)")
	}
	return &useCaseLifecycleAdapter{svc: svc}
}

func (a *useCaseLifecycleAdapter) Upload(ctx context.Context, in voiceover.AssetUploadInput) (voiceover.AssetUploadOutput, error) {
	out, err := a.svc.ProcessAsset(ctx, &lifecycle.FinalizeInput{
		ID:           in.ID,
		Name:         in.Name,
		Filename:     in.Filename,
		Kind:         lifecycle.AssetKindAudio,
		Source:       in.Source,
		LocalPath:    in.LocalPath,
		FolderID:     in.FolderID,
		FolderPath:   in.FolderPath,
		Metadata:     in.Metadata,
		FileHash:     in.FileHash,
		RequireLocal: true,  // use-case path guarantees LocalPath
		RequireHash:  true,  // use-case path supplies FileHash explicitly
		RequireDrive: true,  // upload intent — fail-fast on Drive failure (PR-VO-B1)
		VerifyDB:     false, // use case already runs InsertTx in the same tx
	}, in.FileHash)
	if err != nil {
		return voiceover.AssetUploadOutput{}, fmt.Errorf("voiceover.lifecycle: ProcessAsset: %w", err)
	}
	if out == nil {
		return voiceover.AssetUploadOutput{}, fmt.Errorf("voiceover.lifecycle: ProcessAsset: nil FinalizeResult")
	}
	if !out.OK {
		return voiceover.AssetUploadOutput{}, fmt.Errorf("voiceover.lifecycle: ProcessAsset ok=false: %s", out.Error)
	}
	return voiceover.AssetUploadOutput{
		DriveLink:    out.DriveLink,
		DriveFileID:  out.DriveFileID,
		DownloadLink: out.DownloadLink,
		FileHash:     out.FileHash,
	}, nil
}

var _ voiceover.AssetLifecycle = (*useCaseLifecycleAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverRepository adapter.
//
// Implements voiceover.VoiceoverRepository.InsertTx + DeleteByIDTx via
// tx.ExecContext on the canonical voiceovers table schema (mirrors
// the column set in *assets.VoiceoversRepository.Upsert to avoid
// schema drift). PreReadByID is a no-op stub: the use case at
// usecase.go:188 explicitly ignores the return via `_, _ = ...`, so a
// zero-value response is consistent with the documented behavior.
//
// Schema source-of-truth: internal/infrastructure/database/sqlite/
// assets/voiceovers_repository.go. Adding a column here without a
// SQLite migration will fail at INSERT time, NOT at compile time.
// ─────────────────────────────────────────────────────────────────────

type useCaseRepoAdapter struct {
	db *sql.DB
}

func newUseCaseRepoAdapter(db *sql.DB) *useCaseRepoAdapter {
	if db == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseRepoAdapter: db is required (*sql.DB)")
	}
	return &useCaseRepoAdapter{db: db}
}

// voiceoversTableColumns mirrors *assets.VoiceoversRepository.Upsert
// (single source of truth for the canonical schema). Keep in sync
// during schema migrations — drift here surfaces as runtime
// ExecContext errors, not compile errors.
const voiceoversTableColumns = `
	id, request_id, text_hash, text_preview, language, voice, filename,
	local_path, cleaned_path, folder_id, folder_path, drive_file_id,
	drive_link, download_link, file_hash, duration_seconds, status,
	error, strategy, metadata, created_at, updated_at`

func (a *useCaseRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *voiceover.VoiceoverRecord) error {
	if rec == nil {
		return fmt.Errorf("useCaseRepoAdapter.InsertTx: nil record")
	}
	if rec.UpdatedAt == "" {
		// The use case in usecase.go:271 calls InsertTx AFTER setting
		// rec.UpdatedAt = now; fall back to time.Now() if it ever forgets.
		rec.UpdatedAt = timeutil.FormatRFC3339(time.Now())
	}
	// Atomic UPSERT guarantees (a) the OLD record is never
	// observable AFTER the NEW record, and (b) the caller does not
	// need a separate DeleteByIDTx BEFORE this InsertTx (PR-VO-A2
	// atomic-swap contract — both DELETE and INSERT collapse into
	// one UPSERT with ON CONFLICT(id) DO UPDATE).
	q := `INSERT INTO voiceovers (` + voiceoversTableColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			request_id = excluded.request_id,
			text_hash = excluded.text_hash,
			text_preview = excluded.text_preview,
			language = excluded.language,
			voice = excluded.voice,
			filename = excluded.filename,
			local_path = excluded.local_path,
			cleaned_path = excluded.cleaned_path,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			file_hash = excluded.file_hash,
			status = excluded.status,
			error = excluded.error,
			strategy = excluded.strategy,
			metadata = excluded.metadata,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at`
	_, err := tx.ExecContext(ctx, q,
		rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice,
		rec.Filename, rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath,
		rec.DriveFileID, rec.DriveLink, rec.DownloadLink, rec.FileHash,
		rec.Status, rec.Error, rec.Strategy, rec.Metadata,
		rec.CreatedAt, rec.UpdatedAt,
	)
	return err
}

func (a *useCaseRepoAdapter) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return fmt.Errorf("useCaseRepoAdapter.DeleteByIDTx: empty id")
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM voiceovers WHERE id = ?`, id)
	return err
}

func (a *useCaseRepoAdapter) PreReadByID(ctx context.Context, id string) (*voiceover.VoiceoverRecord, error) {
	// No-op per the use case contract at usecase.go:188 — the use
	// case captures PreReadByID for the eventual Block 7 CONTRACT
	// migration (post-commit orphan-eviction), but the legacy
	// Service.cleanupOrphanVoiceover handles the actual eviction
	// today. Returning (nil, nil) keeps the use case compile-clean
	// without changing observable behavior.
	_ = ctx
	_ = id
	return nil, nil
}

var _ voiceover.VoiceoverRepository = (*useCaseRepoAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// DestinationResolver adapter.
//
// Implements voiceover.DestinationResolver.Resolve over an
// asset.Resolver. Mirrors the production body in *Service.resolveDestination
// (metadata.go) so the legacy + use-case paths route identically:
//
//  1. FORWARD: dest.Group + dest.StyleGroup land in the
//     asset.ResolveRequest (Source = "voiceover" hardcoded).
//  2. MIRROR:  dest.StyleGroup is mirrored verbatim onto the returned
//     ResolvedDestination (resolver is a folder-mapping layer; it
//     does not echo StyleGroup back).
//  3. Nil-safe: nil dest → error; nil resolver → panic at constructor
//     time (fail-fast per AGENTS.md WireUp pattern).
//
// Note: the use case Execute also forwards cmd.Destination itself as a
// ServiceDeps; the asset.ResolveRequest does NOT carry the wire
// Kind / SubfolderName fields. Pre-PR-VO-C1 callers treated the
// resolver as auto-detect; the use case path inherits that semantically
// (PR-VO-B2 / PR-VO-A4 work adds Kind / SubfolderName to resolver
// contracts as the CUTOVER (B-3) step matures — not at P0.1).
// ─────────────────────────────────────────────────────────────────────

type useCaseDestResolverAdapter struct {
	resolver asset.Resolver
}

func newUseCaseDestResolverAdapter(r asset.Resolver) *useCaseDestResolverAdapter {
	if r == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseDestResolverAdapter: resolver is required (asset.Resolver)")
	}
	return &useCaseDestResolverAdapter{resolver: r}
}

func (a *useCaseDestResolverAdapter) Resolve(ctx context.Context, dest *voiceover.DestinationRequest) (*voiceover.ResolvedDestination, error) {
	if dest == nil {
		return nil, fmt.Errorf("useCaseDestResolverAdapter.Resolve: nil DestinationRequest")
	}
	res, err := a.resolver.Resolve(ctx, &asset.ResolveRequest{
		Source:     "voiceover",
		Group:      dest.Group,
		StyleGroup: dest.StyleGroup,
	})
	if err != nil {
		return nil, fmt.Errorf("useCaseDestResolverAdapter.Resolve: resolver failed: %w", err)
	}
	if res == nil {
		// Defensive: a misbehaving resolver may return (nil, nil).
		// Fallback to an empty result so downstream code can proceed
		// with zero-valued folder fields (mirrors the legacy
		// *Service.resolveDestination behavior in metadata.go).
		res = &asset.ResolveResult{}
	}
	return &voiceover.ResolvedDestination{
		Group:      dest.Group,
		FolderID:   res.FolderID,
		FolderPath: res.FolderPath,
		DriveLink:  res.DriveLink,
		StyleGroup: dest.StyleGroup, // MIRROR verbatim (NOT from resolver result).
	}, nil
}

var _ voiceover.DestinationResolver = (*useCaseDestResolverAdapter)(nil)

// timeutil import is used here for FormatRFC3339 fallback in
// InsertTx; the canonical timeutil location avoids bringing in
// time.UTC().Format() boilerplate per Adapter struct.

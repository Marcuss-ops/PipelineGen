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
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
	repo *sqassets.VoiceoversRepository
}

func newUseCaseRepoAdapter(repo *sqassets.VoiceoversRepository) *useCaseRepoAdapter {
	if repo == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseRepoAdapter: repo is required (*sqassets.VoiceoversRepository)")
	}
	return &useCaseRepoAdapter{repo: repo}
}

// toInfraRecord converts the application-layer VoiceoverRecord
// (string-form timestamps for JSON round-trip via job.Payload) to
// the infrastructure-layer Record (time.Time for SQLite-native + a
// DurationSeconds field for forward-compatibility). The two struct
// shapes are NOT identical: voiceover.VoiceoverRecord is the wire
// shape (string timestamps), assets.Record is the SQLite shape
// (time.Time). Keeping the conversion localized here means a future
// schema migration does NOT require touching the converter again —
// the assets.Record surface IS the canonical column set.
func (a *useCaseRepoAdapter) toInfraRecord(rec *voiceover.VoiceoverRecord) *sqassets.Record {
	if rec == nil {
		return nil
	}
	return &sqassets.Record{
		ID:              rec.ID,
		RequestID:       rec.RequestID,
		TextHash:        rec.TextHash,
		TextPreview:     rec.TextPreview,
		Language:        rec.Language,
		Voice:           rec.Voice,
		Filename:        rec.Filename,
		LocalPath:       rec.LocalPath,
		CleanedPath:     rec.CleanedPath,
		FolderID:        rec.FolderID,
		FolderPath:      rec.FolderPath,
		DriveFileID:     rec.DriveFileID,
		DriveLink:       rec.DriveLink,
		DownloadLink:    rec.DownloadLink,
		FileHash:        rec.FileHash,
		Status:          rec.Status,
		Error:           rec.Error,
		Strategy:        rec.Strategy,
		Metadata:        rec.Metadata,
		DurationSeconds: 0, // canonical use case does not track duration today
		CreatedAt:       parseRFC3339OrNow(rec.CreatedAt),
		UpdatedAt:       parseRFC3339OrNow(rec.UpdatedAt),
	}
}

// fromInfraRecord is the inverse of toInfraRecord — used by
// PreReadByID to surface a real (non-stub) row to the use case so
// the post-commit cleanup goroutine can capture orphan paths.
func (a *useCaseRepoAdapter) fromInfraRecord(r *sqassets.Record) *voiceover.VoiceoverRecord {
	if r == nil {
		return nil
	}
	createdAt := ""
	if !r.CreatedAt.IsZero() {
		createdAt = timeutil.FormatRFC3339(r.CreatedAt)
	}
	updatedAt := ""
	if !r.UpdatedAt.IsZero() {
		updatedAt = timeutil.FormatRFC3339(r.UpdatedAt)
	}
	return &voiceover.VoiceoverRecord{
		ID:           r.ID,
		RequestID:    r.RequestID,
		TextHash:     r.TextHash,
		TextPreview:  r.TextPreview,
		Language:     r.Language,
		Voice:        r.Voice,
		Filename:     r.Filename,
		LocalPath:    r.LocalPath,
		CleanedPath:  r.CleanedPath,
		FolderID:     r.FolderID,
		FolderPath:   r.FolderPath,
		DriveFileID:  r.DriveFileID,
		DriveLink:    r.DriveLink,
		DownloadLink: r.DownloadLink,
		FileHash:     r.FileHash,
		Status:       r.Status,
		Error:        r.Error,
		Strategy:     r.Strategy,
		Metadata:     r.Metadata,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
}

// parseRFC3339OrNow parses an RFC3339 timestamp string into time.Time,
// returning time.Now() as a defensive fallback (matches the legacy
// pattern in *assets.VoiceoversRepository.Upsert). Keeps the helper
// private to the adapter to avoid spreading date-parsing helpers
// across packages.
func parseRFC3339OrNow(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	t := timeutil.ParseRFC3339(s)
	if !t.IsZero() {
		return t
	}
	return time.Now()
}

func (a *useCaseRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *voiceover.VoiceoverRecord) error {
	if rec == nil {
		return fmt.Errorf("useCaseRepoAdapter.InsertTx: nil record")
	}
	return a.repo.InsertTx(ctx, tx, a.toInfraRecord(rec))
}

func (a *useCaseRepoAdapter) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return fmt.Errorf("useCaseRepoAdapter.DeleteByIDTx: empty id")
	}
	return a.repo.DeleteByIDTx(ctx, tx, id)
}

func (a *useCaseRepoAdapter) PreReadByID(ctx context.Context, id string) (*voiceover.VoiceoverRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("useCaseRepoAdapter.PreReadByID: empty id")
	}
	r, err := a.repo.PreReadByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("useCaseRepoAdapter.PreReadByID: %w", err)
	}
	if r == nil {
		return nil, nil
	}
	return a.fromInfraRecord(r), nil
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

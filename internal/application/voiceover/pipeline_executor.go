// Package voiceover — pipeline_executor.go (PR-VO-USECASE-PROCESS-DRY,
// P1 in VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-15).
//
// PipelineExecutor is the SINGLE canonical per-item voiceover pipeline
// runner. It replaces the divergent per-item bodies in:
//
//   - usecase.go::processOneLanguage (batch path, pre-DRY): manual
//     TX orchestration (BeginTx → DeleteByIDTx → InsertTx →
//     TransactionalOutbox.EnqueueIndexEvent → Commit). MISSING the
//     dedupe gate (Step 1), media_assets projection (Step 4), and
//     cleanup outbox (Step 6) — silent-success gap (godlike/07
//     "no fake availability" violation that the post-DRY migration
//     closes).
//
//   - process_voiceover_item.go::Execute (per-item path, pre-DRY):
//     delegated to VoiceoverFinalizer (P0.4 Fase 3a, July 2026).
//     Carries all 6 finalization steps correctly.
//
// Post-DRY BOTH callers consume the same PipelineExecutor.RunPipeline
// method. The batch path is migrated to the finalizer (gains the
// dedupe gate + media_assets projection + cleanup outbox that it
// was missing). The per-item path loses its inline TTS/AudioPost/
// Publish/TX code (still uses the finalizer under the hood).
//
// godlike/06 SSOT: PipelineExecutor is the single owner of the
// per-item pipeline. The 5 stage files (stage_synthesize.go etc.
// from PR-VO-STAGES-SPLIT) are file-level owners of legacy batch
// path stages; the PipelineExecutor is the cross-file neutral
// owner that BOTH the batch and per-item callers delegate to.
//
// godlike/07 honest-limitation: this extraction does NOT touch
// the bounded parallel fan-out in usecase.go::Execute (the
// *Executor field). The fan-out layer is the worker-pool; the
// PipelineExecutor is the per-task body. They are distinct
// concerns: fan-out = how to schedule N tasks, PipelineExecutor
// = how to execute ONE task. The bounded pool continues to call
// the use case's processOneTask → processOneLanguage which now
// delegates to the PipelineExecutor.
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ────────────────────────────────────────────────────────────────────────
// PipelineItemInput — neutral DTO consumed by PipelineExecutor
// ────────────────────────────────────────────────────────────────────────

// PipelineItemInput is the neutral DTO consumed by PipelineExecutor.
// Both callers (GenerateVoiceoversUseCase::processOneLanguage and
// ProcessVoiceoverItemUseCase::Execute) populate this DTO from their
// own input types and pass it to RunPipeline.
//
// godlike/06 SSOT: this is the SINGLE canonical shape of the
// per-item pipeline input. Callers do the type translation from
// their input types (VoiceoverItem / GenerateVoiceoverItemCommand)
// into this DTO; the PipelineExecutor reads ONLY this DTO.
//
// godlike/07 minimal-blast-radius: ID + Filename are pre-computed
// by the caller (buildVoiceoverID + BuildVoiceoverFilename). The
// PipelineExecutor trusts them verbatim — it does NOT re-derive
// them, mirroring the BLOC4 P0.6 pass-through invariant pinned
// on the per-item path.
type PipelineItemInput struct {
	// Identity (pre-computed by caller)
	ID        string
	RequestID string
	TextHash  TextHash // typed (PR-VO-TYPED-PRIMITIVES, July 2026)
	Text      string
	Language  Language // typed (PR-VO-TYPED-PRIMITIVES, July 2026)
	Voice     string
	Filename  string
	Strategy  string
	Metadata  map[string]any

	// Behavior flags
	RemoveSilence bool

	// Destination (pre-resolved by caller). The PipelineExecutor
	// does NOT call DestinationResolver — destination resolution
	// is a caller-side concern (usecase.go resolves ONCE per
	// batch; process_voiceover_item.go resolves PER-ITEM with
	// DefaultFolderResolver fallback).
	Dest *ResolvedDestination

	// Replace-mode cleanup context (only for batch path's
	// ShouldSwap=true case; per-item path leaves these empty).
	// The finalizer's Step 6 (cleanup outbox) is guard-skipped
	// when ShouldSwap=false (pre-P0.7 + P0.7 Wave 21 June 2026
	// back-compat: no cleanup event for the per-item path).
	ShouldSwap     bool
	OldDriveFileID string
	OldLocalPath   string
	OldCleanedPath string
}

// ────────────────────────────────────────────────────────────────────────
// PipelineItemDeps — narrow dep surface for the per-item pipeline
// ────────────────────────────────────────────────────────────────────────

// PipelineItemDeps wires the per-item pipeline dependencies.
// All required deps are mandatory (panic on nil — fail-fast per
// AGENTS.md WireUp pattern). AudioPostProcessor is nil-safe
// (only invoked when RemoveSilence is true).
//
// Note: DestinationResolver and DefaultFolderResolver are NOT
// in PipelineItemDeps because destination resolution is a
// caller-side concern (see PipelineItemInput.Dest comment).
//
// Note: TransactionalOutbox is NOT in PipelineItemDeps because
// the finalizer owns the outbox (PR-VO-B3, June 2026). Pre-DRY
// the batch path used TransactionalOutbox directly; post-DRY
// the finalizer handles the index + cleanup outbox events inside
// the same tx.
type PipelineItemDeps struct {
	TTSProvider         TTSProvider
	AudioPostProcessor  AudioPostProcessor // nil-safe
	Publisher           VoiceoverPublisher
	VoiceoverRepository VoiceoverRepository
	Finalizer           VoiceoverFinalizer // mandatory (P0.4 Fase 3a)
	Logger              *zap.Logger
}

// ────────────────────────────────────────────────────────────────────────
// PipelineExecutor — SINGLE canonical per-item pipeline runner
// ────────────────────────────────────────────────────────────────────────

// PipelineExecutor is the SINGLE canonical per-item pipeline runner
// (PR-VO-USECASE-PROCESS-DRY, P1 in VO-DECOMPOSITION-2026-07-04).
//
// godlike/06 SSOT: this struct + its RunPipeline method are the
// ONE canonical owner of the per-item voiceover pipeline. Both
// GenerateVoiceoversUseCase (batch) and ProcessVoiceoverItemUseCase
// (per-item) delegate to it. No other per-item body should exist.
type PipelineExecutor struct {
	deps PipelineItemDeps
}

// NewPipelineExecutor constructs the canonical executor. Mandatory
// deps are fail-fast (panic on nil — per AGENTS.md WireUp pattern).
// AudioPostProcessor is nil-safe.
func NewPipelineExecutor(deps PipelineItemDeps) *PipelineExecutor {
	if deps.TTSProvider == nil {
		panic("voiceover.NewPipelineExecutor: TTSProvider is required (PipelineItemDeps.TTSProvider)")
	}
	if deps.Publisher == nil {
		panic("voiceover.NewPipelineExecutor: Publisher is required (PipelineItemDeps.Publisher)")
	}
	if deps.VoiceoverRepository == nil {
		panic("voiceover.NewPipelineExecutor: VoiceoverRepository is required (PipelineItemDeps.VoiceoverRepository)")
	}
	if deps.Finalizer == nil {
		panic("voiceover.NewPipelineExecutor: Finalizer is required (P0.4 Fase 3a — unified finalization port)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &PipelineExecutor{deps: deps}
}

// RunPipeline runs the canonical 4-stage per-item pipeline:
//
//	Stage 1: TTSProvider.Synthesize
//	Stage 2: AudioPostProcessor.Process (nil-safe, only when RemoveSilence)
//	Stage 3: VoiceoverPublisher.Publish (Drive upload)
//	Stage 4: VoiceoverRepository.BeginTx + VoiceoverFinalizer.Finalize + Commit
//
// Stage 4 delegates to the finalizer which runs all 6 sub-steps
// (dedupe, delete, insert, media_assets projection, index outbox,
// cleanup outbox) inside the caller-owned tx. The PipelineExecutor
// opens the tx, calls Finalize, then commits.
//
// godlike/07 failure mode: every stage returns a VoiceoverItemResult
// with typed Status + Error string. Stage 0 (nil input OR missing
// destination) returns (out, error) where out is non-nil with
// StatusFailed. All other stages also return (out, error) with
// StatusFailed on failure; the caller can wrap the error in a
// typed PipelineError if they need stage classification (the
// per-item path does this; the batch path uses the string error
// directly).
//
// godlike/07 minimal-blast-radius: the error message prefixes
// ("tts_failed:" / "audio_post_process_failed:" / "no_local_payload:"
// / "upload_failed:" / "tx_begin_failed:" / "finalize_failed:" /
// "tx_commit_failed:") are byte-equivalent with the pre-DRY
// per-item path's out.Error strings, so the per-item path's
// prefix-based stage classification continues to work.
func (e *PipelineExecutor) RunPipeline(ctx context.Context, in *PipelineItemInput) (*VoiceoverItemResult, error) {
	// Stage 0: nil-safe + required fields check.
	if in == nil {
		return nil, fmt.Errorf("PipelineExecutor.RunPipeline: nil input")
	}
	if in.Dest == nil || in.Dest.FolderID == "" {
		out := &VoiceoverItemResult{
			Language: in.Language,
			Status:   StatusFailed,
			Error:    "missing_folder_id: voiceover destination has no FolderID for upload",
		}
		return out, fmt.Errorf("%s", out.Error)
	}

	out := &VoiceoverItemResult{
		Language: in.Language,
		Voice:    in.Voice,
		Filename: in.Filename,
		ID:       in.ID,
		Status:   StatusFailed,
	}

	// Stage 1: TTSProvider.Synthesize.
	// P0.2 Fase 2c (July 2026): RemoveSilence is ALWAYS false here.
	// AudioPostProcessor owns silence removal (Stage 2), not the TTS
	// provider. Passing item.RemoveSilence=true to Synthesize would
	// cause the TTS bridge to strip silence inline, and then
	// AudioPostProcessor would re-process an already-cleaned file —
	// double-processing that wastes CPU and risks audio artifacts.
	ttsOut, err := e.deps.TTSProvider.Synthesize(ctx, TTSInput{
		Text:          in.Text,
		Language:      in.Language,
		Voice:         in.Voice,
		Filename:      in.Filename,
		OutputDir:     in.Dest.FolderPath,
		RemoveSilence: false, // P0.2 Fase 2c: never delegate to TTS
	})
	if err != nil {
		out.Error = fmt.Sprintf("tts_failed: %v", err)
		return out, err
	}
	out.LocalPath = ttsOut.LocalPath
	out.CleanedPath = ttsOut.CleanedPath
	if ttsOut.Voice != "" {
		out.Voice = ttsOut.Voice
	}
	out.FileHash = ttsOut.FileHash

	// Stage 2: optional AudioPostProcessor (silence removal). Nil-safe.
	if in.RemoveSilence && e.deps.AudioPostProcessor != nil && ttsOut.LocalPath != "" {
		postOut, err := e.deps.AudioPostProcessor.Process(ctx, AudioPostInput{
			LocalPath: ttsOut.LocalPath,
			OutputDir: in.Dest.FolderPath,
			Filename:  in.Filename,
		})
		if err != nil {
			out.Error = fmt.Sprintf("audio_post_process_failed: %v", err)
			return out, err
		}
		if postOut.CleanedPath != "" {
			out.CleanedPath = postOut.CleanedPath
		}
	}

	if out.LocalPath == "" && out.CleanedPath == "" {
		out.Error = "no_local_payload: TTSProvider + AudioPostProcessor produced no local path"
		return out, fmt.Errorf("%s", out.Error)
	}
	uploadPath := out.CleanedPath
	if uploadPath == "" {
		uploadPath = out.LocalPath
	}

	// Stage 3: VoiceoverPublisher.Publish — populates Drive URLs.
	//
	// PR-VO-USECASE-PROCESS-DRY (July 2026) review-fix #3: the
	// pre-DRY per-item path explicitly injected `style_group` into
	// metaBuf when `!dest.StyleGroup.IsEmpty()`. The new shared
	// PipelineExecutor centralises this logic so BOTH the batch
	// and per-item paths consistently surface `style_group` in
	// the meta JSON. Without this block, the per-item path's
	// meta column would lose the `style_group` field, breaking
	// downstream consumers that read it.
	metaBuf := map[string]any{
		"text_hash":    in.TextHash,
		"text_preview": textutil.Truncate(in.Text, 100),
		"language":     in.Language,
		"voice":        out.Voice,
		"strategy":     in.Strategy,
		"cleaned_path": out.CleanedPath,
	}
	if in.Dest != nil && !in.Dest.StyleGroup.IsEmpty() {
		metaBuf["style_group"] = in.Dest.StyleGroup
	}
	mergeUserMetadata(metaBuf, in.Dest, in.Metadata, e.deps.Logger)
	metaJSON, _ := json.Marshal(metaBuf)

	fileID, err := e.deps.Publisher.Publish(ctx, VoiceoverPublishCommand{
		ID:        in.ID,
		LocalPath: uploadPath,
		Filename:  in.Filename,
		FolderID:  in.Dest.FolderID,
	})
	if err != nil {
		out.Error = fmt.Sprintf("upload_failed: %v", err)
		return out, err
	}
	out.DriveFileID = fileID
	out.DriveLink = CanonicalDriveWebURL(fileID)
	out.DownloadLink = CanonicalDriveDownloadURL(fileID)

	// Stage 4: BeginTx + Finalize + Commit (delegated to finalizer).
	// PR-VO-USECASE-PROCESS-DRY migration: the batch path now
	// delegates to the finalizer (gains the dedupe gate +
	// media_assets projection + cleanup outbox that it was
	// missing pre-DRY). The per-item path was already using the
	// finalizer; the only change is that the body now lives in
	// the PipelineExecutor.
	tx, err := e.deps.VoiceoverRepository.BeginTx(ctx)
	if err != nil {
		out.Error = fmt.Sprintf("tx_begin_failed: %v", err)
		return out, err
	}
	defer func() { _ = tx.Rollback() }() // safe after Commit

	finalizeCmd := &FinalizeCommand{
		ID:             in.ID,
		RequestID:      in.RequestID,
		TextHash:       string(in.TextHash),
		Text:           in.Text,
		Language:       in.Language,
		Voice:          out.Voice,
		Filename:       in.Filename,
		Strategy:       in.Strategy,
		MetaJSON:       metaJSON,
		LocalPath:      out.LocalPath,
		CleanedPath:    out.CleanedPath,
		FileHash:       out.FileHash,
		FolderID:       in.Dest.FolderID,
		FolderPath:     in.Dest.FolderPath,
		DriveFileID:    out.DriveFileID,
		DriveLink:      out.DriveLink,
		DownloadLink:   out.DownloadLink,
		ShouldSwap:     in.ShouldSwap,
		OldDriveFileID: in.OldDriveFileID,
		OldLocalPath:   in.OldLocalPath,
		OldCleanedPath: in.OldCleanedPath,
	}

	finalizeRes, err := e.deps.Finalizer.Finalize(ctx, tx, finalizeCmd)
	if err != nil {
		out.Error = fmt.Sprintf("finalize_failed: %v", err)
		return out, err
	}

	// If the dedupe gate matched an existing row (Reused=true),
	// adopt the matched ID as the canonical identifier.
	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if err := tx.Commit(); err != nil {
		out.Error = fmt.Sprintf("tx_commit_failed: %v", err)
		return out, err
	}

	out.Status = StatusCompleted
	return out, nil
}

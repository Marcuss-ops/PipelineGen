// Package voiceover — process_voiceover_item.go (BLOC5.3 commit-2-child-canonical, June 2026).
//
// Canonical per-item voiceover orchestrator (Pattern 0 — port abstraction
// layer, AGENTS.md). Replaces the legacy process_one.go::ProcessOneVoiceoverUseCase
// bridge (GenerateVoiceoverItemCommand → BatchRequest → Service.GenerateBatch)
// as the SINGLE canonical per-language pipeline, mirroring the per-language
// stage sequence of usecase.go::processOneLanguage (Batch 7-port use case).
//
// BLOC5.3 audit-pin invariants (P0.6 — pass-through, no recalc):
//   - item.TextHash is trusted (pre-computed by fanout via textHashSHA256).
//   - item.Voice is trusted (pre-resolved by fanout from VoiceOverrides[lang]).
//   - item.Filename is trusted (pre-computed by fanout via buildItemFilename).
//   - item.RequestID is trusted (pre-correlates parent → child audit lineage).
//   - NO BatchRequest construction — canonical port-driven pipeline only.
//
// Failure mode contract (godlike/07 — no fake availability): every stage
// returns a VoiceoverItemResult with typed Status + Error string. The
// handler maps the (result, error) tuple into the dispatcher contract:
//   - nil item / nil-validate failure → (*VoiceoverItemResult = nil, error)
//   - per-stage failure             → (*VoiceoverItemResult{failed}, nil)
//   - success                       → (*VoiceoverItemResult{completed}, nil)
//
// Lifecycle atomicity (PR-VO-A2): stage 4 (SQLite swap) is wrapped in a
// single BeginTx/Commit so the DELETE of the OLD row + INSERT of the new
// + outbox EnqueueIndexEvent all commit atomically. The swap tx is
// caller-owned; the use case holds the *sql.Tx across the 3 calls.
package voiceover

import (
	"context"
	"encoding/json"
	"fmt"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ProcessVoiceoverItemDeps wires dependencies for the canonical per-item
// pipeline (Pattern 0, AGENTS.md). All 7 required ports are mandatory —
// the constructor panics on nil per fail-fast WireUp pattern.
// TransactionalOutbox is optional (nil-safe → indexes are silently
// skipped, matching the legacy Pattern 0 tolerance).
//
// FilenameBuilder is required even though Execute trusts item.Filename:
// composition roots use NewDefaultFilenameBuilder when needed, but a
// future BACKFILL stage (idempotency, namespace derivation) will depend
// on the port's surface. Mandatory now keeps the port surface stable.
type ProcessVoiceoverItemDeps struct {
	TTSProvider         TTSProvider
	DestinationResolver DestinationResolver
	AudioPostProcessor  AudioPostProcessor
	Publisher           VoiceoverPublisher
	VoiceoverRepository VoiceoverRepository
	TransactionalOutbox TransactionalOutbox // nil-safe (skip indexing)
	FilenameBuilder     FilenameBuilder
	// DefaultFolderResolver is OPTIONAL (nil-safe). When item.Destination
	// is nil, Execute calls DefaultFolderResolver.Resolve(ctx) to obtain
	// the configured default Voiceover folder. When nil OR the resolver
	// returns ok=false, Execute surfaces a permanent missing-destination
	// error (P0.2 nil-destination fallback, July 2026).
	DefaultFolderResolver VoiceoverDefaultFolderResolver
	// Finalizer is the unified finalization port (P0.4 Fase 3a, July 2026).
	// MANDATORY — the per-item pipeline delegates all 6 finalization steps
	// (dedupe, delete, insert, media_assets projection, index outbox,
	// cleanup outbox) to the finalizer inside a caller-owned tx.
	// Pre-Fase-3a only steps 2-3 + index outbox were executed; dedupe,
	// media_assets, and cleanup were missing from the child pipeline.
	Finalizer VoiceoverFinalizer
	Logger              *zap.Logger // nil-safe via zap.NewNop()
}

// ProcessVoiceoverItemUseCase is the canonical per-item voiceover
// orchestrator. The 7-port typed dep surface keeps the use case free
// of any internal/infrastructure/* import (Pattern 0, June 2026) —
// the composition root satisfies each port by structural conformance
// (Go's implicit-interface rules). The compile-time assertion at the
// bottom of this file pins the narrow VoiceoverItemExecutor interface
// conformance so legacy consumers (promo bridge, future call-site
// migrations) can depend on the interface rather than the concrete.
type ProcessVoiceoverItemUseCase struct {
	deps ProcessVoiceoverItemDeps
}

// NewProcessVoiceoverItemUseCase constructs the canonical use case.
// All required deps are mandatory (panic on nil — fail-fast per
// AGENTS.md WireUp pattern). TransactionalOutbox is the only optional
// dep (nil-safe skip-indexing). Logger is nil-safe via zap.NewNop().
func NewProcessVoiceoverItemUseCase(deps ProcessVoiceoverItemDeps) *ProcessVoiceoverItemUseCase {
	if deps.TTSProvider == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: TTSProvider is required (ProcessVoiceoverItemDeps.TTSProvider)")
	}
	if deps.DestinationResolver == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: DestinationResolver is required")
	}
	if deps.Publisher == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Publisher is required (E1 cutover: drive-only upload)")
	}
	if deps.VoiceoverRepository == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: VoiceoverRepository is required")
	}
	if deps.FilenameBuilder == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: FilenameBuilder is required (port surface kept stable for future BACKFILL stages)")
	}
	if deps.Finalizer == nil {
		panic("voiceover.NewProcessVoiceoverItemUseCase: Finalizer is required (P0.4 Fase 3a — unified finalization port)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ProcessVoiceoverItemUseCase{deps: deps}
}

// Execute runs the canonical 5-stage per-item pipeline:
//
//	Stage 1 (TTS)            → TTSProvider.Synthesize
//	Stage 2 (audio cleanup)  → AudioPostProcessor (only when RemoveSilence)
//	Stage 3 (Drive upload)   → VoiceoverPublisher.Publish
//	Stage 4 (atomic swap)    → VoiceoverRepository.BeginTx + DeleteByIDTx
//	                            + InsertTx + TransactionalOutbox.EnqueueIndexEvent
//
// The destination is resolved once at Stage 0 (before TTS) so the
// stages reuse the same FolderID + FolderPath + StyleGroup. The item
// is validated first (Pre-flight) so a malformed child command
// surfaces at the use case boundary rather than mid-pipeline.
//
// Per the BLOC4 IN-VOICEOVER PASS-THROUGH invariant (P0.6):
//  - item.TextHash is used verbatim (no re-derivation)
//  - item.Voice is used verbatim (no VoiceOverrides re-resolution)
//  - item.Filename is used verbatim (no template re-substitution)
//
// The handler dispatches (result, error) per the godlike/07 contract:
// Stage 0 failures (nil item, validate) return (nil, error). All
// other stages return (out, *PipelineError) with Retryable flag so
// the handler/worker can distinguish transient (TTS/Drive/SQLite busy)
// from permanent (validation, missing destination, bad payload).
// P0.1 Fase 1b (July 2026): every stage failure now returns a typed
// PipelineError instead of (out, nil) — pre-Fase-1b the handler saw
// err==nil and returned (resultMap, nil) → silent false-success.
func (u *ProcessVoiceoverItemUseCase) Execute(ctx context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error) {
	// Pre-flight: nil-safe + validate gate.
	if item == nil {
		return nil, fmt.Errorf("ProcessVoiceoverItemUseCase.Execute: nil item (callers must pass a non-nil *GenerateVoiceoverItemCommand)")
	}
	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("ProcessVoiceoverItemUseCase.Execute: validate (lang=%s, request_id=%s): %w",
			item.Language, item.RequestID, err)
	}

	// Initialize the per-item result envelope. Stage 0 pre-populates
	// what we know; downstream stages fill in URL/Hash/DriveFileID.
	out := &VoiceoverItemResult{
		Language: item.Language,
		Voice:    item.Voice,
		Filename: item.Filename,
		Status:   StatusFailed,
	}

	// Stage 0b: destination resolution. When the caller supplies an
	// explicit destination, resolve it through the canonical resolver.
	// When Destination is nil (e.g. API caller omitted the field),
	// fall back to the configured default Voiceover folder via
	// DefaultFolderResolver (P0.2 nil-destination fallback, July 2026).
	// If the resolver is not wired or returns ok=false, surface a
	// permanent missing-destination error.
	destReq := item.Destination
	if destReq == nil {
		if u.deps.DefaultFolderResolver != nil {
			folderID, _, ok := u.deps.DefaultFolderResolver.Resolve(ctx)
			if ok && folderID != "" {
				// Synthesise a minimal DestinationRequest from the
				// resolved default folder so the downstream resolver
				// and the per-language pipeline see a uniform shape.
				destReq = &DestinationRequest{
					FolderID: folderID,
				}
			} else {
				out.Error = "missing_destination: no default Voiceover folder configured (resolve returned ok=false)"
				return out, newPipelineError(StageDestinationResolve, false, fmt.Errorf("%s", out.Error))
			}
		} else {
			out.Error = "missing_destination: GenerateVoiceoverItemCommand.Destination is nil and DefaultFolderResolver is not wired"
			return out, newPipelineError(StageDestinationResolve, false, fmt.Errorf("%s", out.Error))
		}
	}
	dest, err := u.deps.DestinationResolver.Resolve(ctx, destReq)
	if err != nil {
		out.Error = fmt.Sprintf("destination_resolve_failed: %v", err)
		return out, newPipelineError(StageDestinationResolve, false, err)
	}
	if dest == nil || dest.FolderID == "" {
		out.Error = "missing_folder_id: voiceover destination has no FolderID for upload"
		return out, newPipelineError(StageDestinationResolve, false, fmt.Errorf("%s", out.Error))
	}

	// Trust item.TextHash from fanout (P0.6 invariant — no re-derive).
	itemHash := item.TextHash

	// ID is derived deterministically from (textHash, language, folderID).
	id := buildVoiceoverID(itemHash, item.Language, dest.FolderID)
	out.ID = id

	// Stage 1: TTSProvider.Synthesize (Stage 0 uses ttsProvider, Stage 1+ runs).
	ttsOut, err := u.deps.TTSProvider.Synthesize(ctx, TTSInput{
		Text:          item.Text,
		Language:      item.Language,
		Voice:         item.Voice,
		Filename:      item.Filename,
		OutputDir:     dest.FolderPath,
		RemoveSilence: item.RemoveSilence,
	})
	if err != nil {
		out.Error = fmt.Sprintf("tts_failed: %v", err)
		u.deps.Logger.Warn("voiceover.processItem: stage 1 TTS failed",
			zap.String("language", item.Language),
			zap.String("request_id", item.RequestID),
			zap.Error(err))
		return out, newPipelineError(StageTTS, true, err)
	}
	out.LocalPath = ttsOut.LocalPath
	out.CleanedPath = ttsOut.CleanedPath
	if ttsOut.Voice != "" {
		out.Voice = ttsOut.Voice
	}
	out.FileHash = ttsOut.FileHash

	// Stage 2: optional AudioPostProcessor (silence removal). Nil-safe: only
	// invoked when RemoveSilence is true AND the processor is wired.
	if item.RemoveSilence && u.deps.AudioPostProcessor != nil && ttsOut.LocalPath != "" {
		postOut, err := u.deps.AudioPostProcessor.Process(ctx, AudioPostInput{
			LocalPath: ttsOut.LocalPath,
			OutputDir: dest.FolderPath,
			Filename:  item.Filename,
		})
		if err != nil {
			out.Error = fmt.Sprintf("audio_post_process_failed: %v", err)
			u.deps.Logger.Warn("voiceover.processItem: stage 2 audio_post failed",
				zap.String("language", item.Language),
				zap.String("request_id", item.RequestID),
				zap.Error(err))
			return out, newPipelineError(StageAudioPost, false, err)
		}
		if postOut.CleanedPath != "" {
			out.CleanedPath = postOut.CleanedPath
		}
	}

	if out.LocalPath == "" && out.CleanedPath == "" {
		out.Error = "no_local_payload: TTSProvider + AudioPostProcessor produced no local path"
		return out, newPipelineError(StageTTS, false, fmt.Errorf("%s", out.Error))
	}
	uploadPath := out.CleanedPath
	if uploadPath == "" {
		uploadPath = out.LocalPath
	}

	// Stage 3: VoiceoverPublisher.Publish — populates Drive URLs (canonical fileID then URL helpers).
	metaBuf := map[string]any{
		"text_hash":    itemHash,
		"text_preview": textutil.Truncate(item.Text, 100),
		"language":     item.Language,
		"voice":        out.Voice,
		"strategy":     string(item.Strategy),
		"cleaned_path": out.CleanedPath,
	}
	if dest.StyleGroup != "" {
		metaBuf["style_group"] = dest.StyleGroup
	}
	if item.Metadata != nil {
		mergeUserMetadata(metaBuf, dest, item.Metadata, u.deps.Logger)
	}
	metaJSON, _ := json.Marshal(metaBuf)

	fileID, err := u.deps.Publisher.Publish(ctx, VoiceoverPublishCommand{
		ID:        id,
		LocalPath: uploadPath,
		Filename:  item.Filename,
		FolderID:  dest.FolderID,
	})
	if err != nil {
		out.Error = fmt.Sprintf("upload_failed: %v", err)
		u.deps.Logger.Warn("voiceover.processItem: stage 3 publisher.Publish failed",
			zap.String("language", item.Language),
			zap.String("request_id", item.RequestID),
			zap.Error(err))
		return out, newPipelineError(StageUpload, true, err)
	}
	out.DriveFileID = fileID
	out.DriveLink = CanonicalDriveWebURL(fileID)
	out.DownloadLink = CanonicalDriveDownloadURL(fileID)

	// Stage 4: Unified atomic finalization via VoiceoverFinalizer
	// (P0.4 Fase 3a, July 2026). Replaces the previous inline
	// BeginTx → DeleteByIDTx → InsertTx → outbox → Commit sequence
	// with a single call to the finalizer which runs all 6 steps
	// (dedupe, delete, insert, media_assets projection, index
	// outbox, cleanup outbox) inside a caller-owned tx.
	//
	// Pre-Fase-3a: steps 1 (dedupe), 4 (media_assets), and 6
	// (cleanup) were MISSING from the child pipeline. The legacy
	// batch path (finalizeStage) had all 6. Post-Fase-3a both
	// paths share the same finalizer.
	tx, err := u.deps.VoiceoverRepository.BeginTx(ctx)
	if err != nil {
		out.Error = fmt.Sprintf("tx_begin_failed: %v", err)
		u.deps.Logger.Warn("voiceover.processItem: stage 4 tx_begin failed",
			zap.String("language", item.Language),
			zap.String("request_id", item.RequestID),
			zap.Error(err))
		return out, newPipelineError(StageTxBegin, true, err)
	}
	defer func() { _ = tx.Rollback() }() // safe after Commit

	finalizeCmd := &FinalizeCommand{
		ID:             id,
		RequestID:      item.RequestID,
		TextHash:       itemHash,
		Text:           item.Text,
		Language:       item.Language,
		Voice:          out.Voice,
		Filename:       item.Filename,
		Strategy:       string(item.Strategy),
		MetaJSON:       metaJSON,
		LocalPath:      out.LocalPath,
		CleanedPath:    out.CleanedPath,
		FileHash:       out.FileHash,
		FolderID:       dest.FolderID,
		FolderPath:     dest.FolderPath,
		DriveFileID:    out.DriveFileID,
		DriveLink:      out.DriveLink,
		DownloadLink:   out.DownloadLink,
		// Per-item path (child pipeline): no old-row swap context.
		// ShouldSwap stays false (no cleanup event).
		ShouldSwap:     false,
		OldDriveFileID: "",
		OldLocalPath:   "",
		OldCleanedPath: "",
	}

	finalizeRes, err := u.deps.Finalizer.Finalize(ctx, tx, finalizeCmd)
	if err != nil {
		out.Error = fmt.Sprintf("finalize_failed: %v", err)
		u.deps.Logger.Warn("voiceover.processItem: stage 4 finalize failed",
			zap.String("language", item.Language),
			zap.String("request_id", item.RequestID),
			zap.Error(err))
		return out, newPipelineError(StageTxCommit, true, err)
	}

	// If the dedupe gate matched an existing row (Reused=true),
	// adopt the matched ID as the canonical identifier.
	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if err := tx.Commit(); err != nil {
		out.Error = fmt.Sprintf("tx_commit_failed: %v", err)
		return out, newPipelineError(StageTxCommit, true, err)
	}

	out.Status = StatusCompleted
	u.deps.Logger.Info("voiceover.processItem: success",
		zap.String("language", item.Language),
		zap.String("request_id", item.RequestID),
		zap.String("id", out.ID),
		zap.String("drive_link", out.DriveLink))
	return out, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): the production concrete
// *ProcessVoiceoverItemUseCase must structurally satisfy the narrow
// VoiceoverItemExecutor port. Drift between Execute's signature and the
// port contract triggers a compile error at this line.
var _ VoiceoverItemExecutor = (*ProcessVoiceoverItemUseCase)(nil)

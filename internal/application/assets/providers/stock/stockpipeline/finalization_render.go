package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// Package stockpipeline — finalizer_gates_verify.go — fail-closed gates.
//
// VerifyChunks and VerifyMetadata are the §12-1 validation gates that
// reject incomplete chunk/metadata states before BuildFinalizationRequest
// composes the canonical FinalizationRequest.
// VerifyChunks is the §12-1 fail-closed gate for chunked outputs.
// Pure function — easy TDD. Composition order:
//
//  1. empty chunks → ErrStockNoChunksFinalized
//  2. missing LocalPath on any chunk → ErrStockChunkNotFinalized
//  3. empty RemoteFileID on any chunk → ErrStockChunkNotFinalized
//  4. empty SHA256 on any chunk → ErrStockChunkHashMissing
//  5. malformed SHA256 on any chunk (len<64 / non-hex / uppercase) →
//     ErrStockChunkHashInvalid (Commit 0.2 P0 2.4 hardening)
//
// Order matters for the test assertion table (each test isolates
// one rule, not the chain).
//
// Commit 0.2 (godlike/07 fail-closed at the gate layer): SHA256
// strict-format validation is enforced here so the
// BuildFinalizationRequest IdempotencyKey derivation
// (prefix + sha[:16]) is no longer reachable on a short hash,
// eliminating the verdict's P0 #3 panic class.
func VerifyChunks(chunks []ChunkState) error {
	if len(chunks) == 0 {
		return ErrStockNoChunksFinalized
	}
	for _, c := range chunks {
		if c.LocalPath == "" {
			return fmt.Errorf("%w: chunk[%d] (artifact=%s) LocalPath empty",
				ErrStockChunkNotFinalized, c.Index, c.ArtifactID)
		}
		if c.RemoteFileID == "" {
			return fmt.Errorf("%w: chunk[%d] (artifact=%s) RemoteFileID empty",
				ErrStockChunkNotFinalized, c.Index, c.ArtifactID)
		}
		if c.SHA256 == "" {
			return fmt.Errorf("%w: chunk[%d] (artifact=%s) SHA256 must be computed BEFORE publish (P0 2.4)",
				ErrStockChunkHashMissing, c.Index, c.ArtifactID)
		}
		// Commit 0.2 P0 2.4 hardening: reject malformed SHA256 BEFORE
		// the panic site at BuildFinalizationRequest's composition.
		// Errors.Is(asset.ErrSHA256Invalid, ...) AND
		// errors.Is(ErrStockChunkHashInvalid, ...) both surface so
		// callers can probe either sentinel.
		if _, err := asset.ValidateSHA256(c.SHA256); err != nil {
			// godlike/07 typed-error contract (Commit 0.2 P0 2.4):
			// errors.Join preserves BOTH sentinels so callers can
			// errors.Is(ErrStockChunkHashInvalid) AND
			// errors.Is(asset.ErrSHA256Invalid) — fmt.Errorf supports
			// only one %w, so Join is the canonical multi-sentinel carrier.
			return errors.Join(
				ErrStockChunkHashInvalid,
				fmt.Errorf("chunk[%d] (artifact=%s)", c.Index, c.ArtifactID),
				err,
			)
		}
	}
	return nil
}

// VerifyMetadata is the §12-1 fail-closed gate for the per-run
// metadata.json. Symmetric to VerifyChunks but with metadata-specific
// flags. Pure function. Commit 0.2 hardening: SHA256 strict-format
// validation surfaces ErrStockMetadataHashInvalid for malformed
// digest inputs (len<64 / non-hex / uppercase) — same defence-in-depth
// contract as VerifyChunks.
func VerifyMetadata(m MetadataState) error {
	if m.LocalPath == "" {
		return fmt.Errorf("%w: LocalPath empty",
			ErrStockMetadataNotPublished)
	}
	if m.RemoteFileID == "" {
		return fmt.Errorf("%w: RemoteFileID empty (publish failed or missing)",
			ErrStockMetadataNotPublished)
	}
	if m.SHA256 == "" {
		return fmt.Errorf("%w: SHA256 must be computed BEFORE publish (P0 2.4)",
			ErrStockMetadataNotPublished)
	}
	// Commit 0.2 P0 2.4 hardening: malformed-SHA256 → ErrStockMetadataHashInvalid.
	if _, err := asset.ValidateSHA256(m.SHA256); err != nil {
		// godlike/07 typed-error: errors.Join preserves both sentinels.
		return errors.Join(ErrStockMetadataHashInvalid, err)
	}
	return nil
}

const stockProcessType = "stock"

func startStockPhase(ctx context.Context, _ StepRunner, phase string) *kernobs.StageHandle {
	return kernobs.BeginStage(ctx, kernobs.StageName(phase))
}
func finishStockPhase(runner StepRunner, h *kernobs.StageHandle, phase string, err error) {
	if h == nil {
		return
	}
	report := h.End(err)
	if err != nil && runner != nil && runner.Log() != nil {
		runner.Log().Warn("stock: canonical process phase observation failed", zap.String("phase", phase), zap.String("status", report.Status), zap.Error(err))
	}
}
func startServiceStockPhase(ctx context.Context, phase, _ string) *kernobs.StageHandle {
	return kernobs.BeginStage(ctx, kernobs.StageName(phase))
}
func finishServiceStockPhase(log *zap.Logger, h *kernobs.StageHandle, err error) {
	if h == nil {
		return
	}
	report := h.End(err)
	if err != nil && log != nil {
		log.Warn("stock: canonical process phase observation failed", zap.String("status", report.Status), zap.Error(err))
	}
}
func prepareStockDriveArtifact(ctx context.Context, runner StepRunner, artifact finalization.VerifiedArtifact, _ map[string]any) (published finalization.PublishedArtifact, err error) {
	prepare := func(operationCtx context.Context) error {
		published, err = runner.ArtifactPreparation().Prepare(operationCtx, artifact)
		return err
	}
	if run := kernobs.FromContext(ctx); run != nil {
		err = run.Operation(ctx, kernobs.OperationInfo{
			Stage:     kernobs.StagePublish,
			Component: kernobs.ComponentDrive,
			Operation: kernobs.OperationUpload,
			Items:     1,
			Bytes:     artifact.SizeBytes,
		}, prepare)
		return published, err
	}
	return published, prepare(ctx)
}
func boolToInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func sumPlanDuration(plans []ClipPlan) float64 {
	var total float64
	for _, p := range plans {
		if p.EndSec > p.StartSec {
			total += p.EndSec - p.StartSec
		}
	}
	return total
}
func sumChunkDuration(chunks []ChunkState) float64 {
	var total float64
	for _, c := range chunks {
		if c.EndSec > c.StartSec {
			total += c.EndSec - c.StartSec
		}
	}
	return total
}

// Package stockpipeline — render_ports.go (PR-SPLIT-RENDER-PORTS, August 2026).
//
// Slim orchestrator carrying the 2 main ports (StockRenderer + VideoCutter)
// + their no-op test fixtures + the 2 compile-time pins. Extracted from
// the 569 LoC monolith per AGENTS.md Pattern 5 + godlike/06 SSOT
// one-canonical-owner-per-fact (precedent: PR-SPLIT-FINALIZE-TYPES-V2 +
// PR-SPLIT-LEGACYAUDIT-V2 + PR-SPLIT-READYZ-CHECKERS + PR-SPLIT-LLM-CLIENT
// + PR-SPLIT-STEP-EXTRACT-CLIPS + PR-SPLIT-ENRICHMENT-HANDLER — 7th
// LONG-FILES-DECOMPOSITION-V2-EXEC closure).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - StockRenderer + VideoCutter interfaces live ONLY in this file.
//   - The 2 no-op test fixtures (noOpRenderer + noOpCutter) live ONLY in
//     this file — they implement the ports declared here.
//   - ErrNoOpCutter typed sentinel lives ONLY in this file.
//   - The 2 compile-time pins (var _ StockRenderer = (*noOpRenderer)(nil)
//   - var _ VideoCutter = (*noOpCutter)(nil)) live ONLY in this file.
//   - The neutral DTOs (RenderRequest + RenderResult + CutRequest +
//     CutJob + CutItemStatus + CutItemResult + CutBatchResult + Clip)
//     live ONLY in render_dto.go (sister file).
//   - The transition catalog (TransitionSegment + TransitionRenderer +
//     Transition + TransitionRegistry) lives ONLY in
//     render_transitions.go (sister file).
//   - SourceDurationProbe (the pre-cut duration validation port) lives
//     ONLY in render_dto.go (grouped with the cutter DTOs since it's
//     used by step_extract_clips to validate before invoking
//     VideoCutter.Cut).
//
// Import-boundary invariant (verified by `go vet`):
//
//	go vet ./internal/application/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/infrastructure/process`. Both are infra concerns; the app
// layer only depends on the StockRenderer + VideoCutter ports
// declared here.
// ── StockRenderer port (PR6, June 2026) ────────────────────────────────

// StockRenderer is the canonical port the application layer uses to render
// a chunk of stock clips into a single output video. The interface is
// deliberately minimal: a single Render method that takes a neutrally-
// typed RenderRequest (declared in render_dto.go) and returns a
// RenderResult (declared in render_dto.go).
//
// Implementations live in `internal/infrastructure/media/render/`.
type StockRenderer interface {
	// Render concatenates (and optionally decorates) the input clips into
	// the output video file at OutputPath, honouring the request's
	// transitions/effects/encoding policy.
	Render(ctx context.Context, req RenderRequest) (RenderResult, error)
}

// ── VideoCutter port (PR6) ─────────────────────────────────────────────

// VideoCutter extracts multiple clips from a single source video. The
// port encapsulates the batch-vs-fallback-to-individual branching,
// per-job on-disk verification, and ffprobe-driven validity gating.
// Callers receive a structured CutBatchResult (declared in
// render_dto.go) carrying per-job outcomes in input-Jobs order (see
// CutBatchResult invariant documentation). Implementations live in
// `internal/infrastructure/media/render/`.
type VideoCutter interface {
	// Cut extracts N clips from a single source video. The batch of
	// jobs shares the same SourcePath; encoding policy (codec / preset
	// / crf / audio) is uniform across the batch.
	//
	// Returned CutBatchResult enforces the "mai nil con zero output"
	// invariant: len(Items) == len(req.Jobs) ALWAYS, with failed
	// Items populated as Status=CutItemStatusFailed + JobID +
	// Err (never nil with no Items).
	//
	// Top-level error semantics:
	//   - All Items failed               → non-nil error + Items
	//                                       (all Status=Failed)
	//   - Partial success (≥1 succeeded) → nil error + Items
	//                                       (mixed Succeeded / Validated /
	//                                       ProbeFailed / Failed)
	//   - Empty Jobs                     → nil error + empty Items
	//
	// Callers should partition via SuccessfulItems / FailedItems
	// accessors rather than relying on the top-level error alone.
	Cut(ctx context.Context, req CutRequest) (CutBatchResult, error)
}

// ── No-op test fixtures (co-located with the ports they implement) ───

// _ ensures StockRenderer stays a true interface (no accidental struct
// embedding on the application side).
var _ StockRenderer = (*noOpRenderer)(nil)

type noOpRenderer struct{}

func (noOpRenderer) Render(ctx context.Context, req RenderRequest) (RenderResult, error) {
	return RenderResult{}, nil
}

// _ ensures VideoCutter stays a true interface.
var _ VideoCutter = (*noOpCutter)(nil)

type noOpCutter struct{}

func (noOpCutter) Cut(ctx context.Context, req CutRequest) (CutBatchResult, error) {
	// Return a fully-populated (but empty-result) batch so the
	// "mai nil con zero output" invariant holds — Items has
	// len(req.Jobs) entries each with Status=CutItemStatusFailed
	// and Err=ErrNoOpCutter. Tests can iterate Items without nil
	// checks. The top-level error wraps the per-item sentinel so
	// callers preserving the cutErr != nil distinction keep
	// working unchanged.
	items := make([]CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		items[i] = CutItemResult{
			JobID:  j.OutputPath,
			Status: CutItemStatusFailed,
			Err:    ErrNoOpCutter,
		}
	}
	return CutBatchResult{
		SourcePath: req.SourcePath,
		Items:      items,
	}, ErrNoOpCutter
}

// ErrNoOpCutter is the per-item failure sentinel the no-op
// implementation returns for every Item (and as the batch-level
// error). Distinct ErrCutFailed wording so callers can
// errors.Is on no-op failures vs real ffmpeg failures.
var ErrNoOpCutter = errors.New("cutter: noOpCutter (test fixture)")

// Package stockpipeline — render.go refactored (PR6, June 2026).
//
// Pre-PR6: renderChunk lived inside the application package and directly
// constructed the FFmpeg filter_complex chain, the 14-string transitions
// table, and the per-codec encoding arguments. It also called process.Run
// to dispatch the resulting command line. All FFmpeg + execution knowledge
// leaked into the application layer (violates AGENTS.md Pattern 0 + 8).
//
// Post-PR6: this file is a PURE DECISION MODULE. It inspects the
// application-side rendering policy (clips, every-Nth transitions, every-
// Nth overlays, encoding params) and produces a neutral
// `stock.RenderRequest` that the canonical `StockRenderer` port consumes.
// The FFmpeg-specific code lives in
// `internal/infrastructure/media/render/ffmpeg.go` (see package docs).
//
// Import-boundary invariant:
//
//	go vet ./internal/application/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/infrastructure/process`. This file respects the invariant.
// renderChunk is the app-layer decision entry point for chunk rendering.
// It translates the application flags (noTransitions, noEffects, noAudio)
// into a generic stock.RenderRequest and delegates execution to s.renderer.
//
// Behavioural equivalence with pre-PR6:
//   - noTransitions && noEffects → renderer picks concat-demuxer fast path
//   - noTransitions only OR noEffects only → filter_complex with one branch
//   - full policy → filter_complex with Nth-clip transitions + Nth-clip overlays
//
// Titles are currently unused by the FFmpeg impl (kept in the signature
// for downstream API compatibility — the future subtitle overlay path will
// consume them).
func (s *Service) renderChunk(ctx context.Context, clips []string, titles []string, outputPath string, noTransitions, noEffects, noAudio bool, chunkIdx int) error {
	if len(clips) == 0 {
		return fmt.Errorf("renderChunk: no clips to render")
	}
	if s.renderer == nil {
		return fmt.Errorf("renderChunk: StockRenderer port is nil — was composition root build correct?")
	}

	cfg := DefaultPipelineConfig()
	req := RenderRequest{
		OutputPath: outputPath,
		InputPaths: clips,
		Width:      cfg.Width,
		Height:     cfg.Height,
		FPS:        cfg.FPS,
		// The infrastructure renderer supplies the configured runtime policy;
		// leaving Codec empty keeps this neutral application DTO from making a
		// second encoder decision.
		Codec:            "",
		Preset:           cfg.Preset,
		CRF:              cfg.CRF,
		KeyframeInterval: cfg.KeyframeInterval,
		KeepAudio:        !noAudio,

		NoTransitions:   noTransitions,
		TransitionEvery: cfg.TransitionInterval,
		ClipDurationSec: s.runtime.ClipDurationSec,

		NoEffects:       noEffects,
		EffectsDir:      cfg.EffectsDir,
		EffectEvery:     cfg.EffectInterval,
		EffectIndexHint: chunkIdx % 1024, // deterministic hint (mod 1024 avoids overflow on big indexes)
		OverlayOpacity:  cfg.OverlayOpacity,

		Logger:     s.log,
		ChunkIndex: chunkIdx,
	}

	resolved, err := ResolveRenderPlan(req)
	if err != nil {
		return fmt.Errorf("renderChunk resolve plan failed: %w", err)
	}
	res, err := s.renderer.Render(ctx, resolved)
	if err != nil {
		return fmt.Errorf("renderChunk port failed: %w", err)
	}

	// Telemetry projection: the result carries operational metrics the
	// caller may want for logging downstream (which transitions actually
	// fired, how long it took). This data is informational only; the
	// rendered file at req.OutputPath is the primary artifact.
	s.log.Info("stock render: completed via port",
		zap.Int("chunk", chunkIdx),
		zap.Bool("fast_path", res.UsedFastPath),
		zap.Int64("duration_ms", res.DurationMS),
		zap.Strings("transitions", res.AppliedTransitions),
		zap.Strings("overlays", res.AppliedOverlayFiles),
	)
	return nil
}

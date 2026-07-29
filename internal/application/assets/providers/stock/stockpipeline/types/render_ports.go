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
package types

import (
	"context"
	"errors"
)

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

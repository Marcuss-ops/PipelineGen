// Package stockpipeline — render_ports.go (PR-SPLIT-STOCK-PORTS, July 2026).
//
// Owns the application-layer render + cutter + transition + clip surface
// (canonical Pattern 0 ports + neutral DTOs + no-op test fixtures).
// Extracted from ports.go per godlike/06 SSOT one-canonical-owner-per-fact:
// this file is the SOLE canonical owner of the StockRenderer + VideoCutter
// interfaces, the request/result DTOs, the transition catalog, the per-clip
// Clip DTO, and the no-op test fixtures (noOpRenderer + noOpCutter +
// ErrNoOpCutter + the 2 var _ compile-time pins).
//
// Import-boundary invariant (verified by `go vet`):
//
//	go vet ./internal/application/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/infrastructure/process`. Both are infra concerns; the app layer
// only depends on the StockRenderer + VideoCutter ports defined here.
package stockpipeline

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// ── StockRenderer port (PR6, June 2026) ────────────────────────────────

// StockRenderer is the canonical port the application layer uses to render
// a chunk of stock clips into a single output video. The interface is
// deliberately minimal: a single Render method that takes a neutrally-
// typed RenderRequest and returns a RenderResult.
//
// Implementations live in `internal/infrastructure/media/render/`.
type StockRenderer interface {
	// Render concatenates (and optionally decorates) the input clips into
	// the output video file at OutputPath, honouring the request's
	// transitions/effects/encoding policy.
	Render(ctx context.Context, req RenderRequest) (RenderResult, error)
}

// ── Render DTOs (application-layer neutral types) ─────────────────────

// RenderRequest is a neutrally-typed request the application layer
// passes to StockRenderer.Render. No FFmpeg-specific types leak.
type RenderRequest struct {
	// OutputPath is the absolute path where the rendered chunk must be
	// written. The renderer creates intermediate temp files and deletes
	// them on success.
	OutputPath string

	// InputPaths are the absolute paths of the input clips (in order).
	// The renderer concatenates them in the supplied order.
	InputPaths []string

	// ── Encoding policy (target codec + container) ────────────────
	Width            int
	Height           int
	FPS              int
	Codec            string // "libx264", "h264_nvenc", ...
	Preset           string
	CRF              int
	KeyframeInterval int
	KeepAudio        bool // false = strip audio output (-an)

	// ── Transition policy ─────────────────────────────────────────
	NoTransitions   bool // skip transitions entirely (fast path eligible)
	TransitionEvery int  // apply transition at every Nth clip boundary (1=every)
	ClipDurationSec int  // clip target duration (used to compute fadeStart)

	// ── Effects policy ─────────────────────────────────────────────
	NoEffects       bool   // skip overlay effects
	EffectsDir      string // directory scanned for .mp4 overlay files
	EffectEvery     int    // apply overlay every Nth clip
	EffectIndexHint int    // 0+ — deterministic hint for selecting effect file
	OverlayOpacity  float64

	// ── Logging / telemetry ────────────────────────────────────────
	Logger     *zap.Logger
	ChunkIndex int // for log enrichment
}

// RenderResult is the neutral result returned by StockRenderer.Render.
// It is informational — callers can use it for telemetry + testing but
// the main artifact is the rendered file at req.OutputPath.
type RenderResult struct {
	// UsedFastPath is true when the renderer skipped the filter_complex
	// chain and used the concat demuxer (no transitions + no effects +
	// ≥2 inputs).
	UsedFastPath bool

	// AppliedTransitions lists the catalog Names of transitions actually
	// emitted (empty when NoTransitions or UsedFastPath).
	AppliedTransitions []string

	// AppliedOverlayFiles lists the .mp4 effect files actually overlaid
	// (empty when NoEffects or UsedFastPath).
	AppliedOverlayFiles []string

	// DurationMS is the wall-clock duration of the render (informational).
	DurationMS int64
}

// ── Transition Registry ────────────────────────────────────────────────

// TransitionSegment is the temporal segment a transition applies to.
// End-side fades the END of a clip; Start-side fades the START of the
// NEXT clip. Together they form the visual handoff at a clip boundary.
type TransitionSegment int

const (
	// SegmentEnd applies the filter to the END portion of the current
	// clip (typically a fade-out or build-up transition like boxblur).
	SegmentEnd TransitionSegment = iota

	// SegmentStart applies the filter to the START portion of the next
	// clip (typically a fade-in or un-build transition).
	SegmentStart
)

// TransitionRenderer is the func-format a single transition uses to emit
// its FFmpeg filter chain. clipDurationSec is the canonical clip length
// (used to position the transition inside the END-side clip).
//
// The closure approach (vs string templates) per PR6 design allows each
// transition to encapsulate its own asymmetric filter syntax — e.g. `fade`
// uses `st=%f:d=%f` whereas `blur` uses `boxblur=15:enable='gt(t,%f)'`.
type TransitionRenderer func(clipDurationSec int) string

// Transition is a single named transition entry in the catalog.
type Transition struct {
	Name string

	// RenderEnd is the FFmpeg filter fragment applied to the END of a
	// clip (typically the fade-INTO the next clip).
	RenderEnd TransitionRenderer

	// RenderStart is the FFmpeg filter fragment applied to the START of
	// the next clip (typically the fade-FROM the previous clip).
	RenderStart TransitionRenderer

	// Description is a one-line human label for telemetry.
	Description string
}

// TransitionRegistry exposes the catalogue of transitions available to
// the StockRenderer port. The application layer composes transitions
// via this interface; the infra implementation reads it during
// filter_complex construction.
//
// Implementations are expected to be effectively read-only (Register*
// is for catalog extension during bootstrap); the infra renderer
// consults All()/Get() during Render().
type TransitionRegistry interface {
	// All returns all registered transitions in stable (insertion) order.
	All() []Transition

	// Get returns the transition registered under the given name, or
	// (Transition{}, false) when missing.
	Get(name string) (Transition, bool)

	// Len returns the number of registered transitions.
	Len() int
}

// ── SourceDurationProbe port (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026) ──

// SourceDurationProbe is the canonical port the stock pipeline
// uses to probe a source video's duration in seconds BEFORE
// invoking VideoCutter.Cut. The probe lets step_extract_clips
// validate each ClipPlan's EndSec against the source duration
// (godlike/07 NO-FAKE-AVAILABILITY: a clip with EndSec > source
// duration cannot be cut by ffmpeg; the prior silent-success path
// would have written a half-broken artifact to Drive). The probe
// is OPTIONAL in the composition root: when nil, step_extract_clips
// falls back to StagedAsset.DurationSec (populated by the upstream
// stage_sources step when known) or skips the bounds check
// (godlike/07 minimum-blast-radius: backward-compat for test
// fixtures and legacy composition roots that haven't wired the
// probe yet; PR-STOCK-SOURCE-DURATION-WIRE is the forward-pointer
// for production wiring).
//
// Implementations live in `internal/infrastructure/media/probe/`
// (ffprobe-backed) or `internal/application/youtube/usecase/`
// (yt-dlp --print-duration-backed). The port is declared on the
// application layer per AGENTS.md Pattern 0; the infrastructure
// concrete is injected via composition root.
type SourceDurationProbe interface {
	// ProbeDurationSec returns the source video's duration in
	// seconds (float, fractional). The probe is read-only (does
	// not mutate the source); a returned error means the probe
	// could not determine the duration (e.g. ffprobe subprocess
	// failed, or the file is not a recognizable video container).
	// On error, step_extract_clips logs Warn and falls through to
	// the unvalidated path (godlike/07 fail-open) so transient
	// probe failures don't break the whole pipeline.
	ProbeDurationSec(ctx context.Context, sourcePath string) (float64, error)
}

// ── VideoCutter port (PR6) ─────────────────────────────────────────────

// VideoCutter extracts multiple clips from a single source video. The
// port encapsulates the batch-vs-fallback-to-individual branching,
// per-job on-disk verification, and ffprobe-driven validity gating.
// Callers receive a structured CutBatchResult carrying per-job
// outcomes in input-Jobs order (see CutBatchResult invariant
// documentation). Implementations live in
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

// CutRequest is the neutral application-layer request the application
// passes to VideoCutter.Cut. Encoding parameters apply to every job in
// the batch (per-job encoding overrides are deliberately omitted —
// stockpipeline is monotonic in source video).
type CutRequest struct {
	// SourcePath is the absolute path of the source video all jobs cut
	// from (e.g. one yt-dlp download window).
	SourcePath string

	// Jobs is the per-clip extraction list, in stable order. Caller
	// must ensure OutputPaths are unique (the adapter will skip
	// duplicates silently rather than overwrite).
	Jobs []CutJob

	// ── Batch-level encoding policy ────────────────────────────────
	Codec   string // "libx264", "h264_nvenc", ...
	Preset  string
	CRF     int
	NoAudio bool

	// ── Logging / telemetry ────────────────────────────────────────
	Logger    *zap.Logger
	SourceIdx int // for log enrichment (matches caller-chosen index)
}

// CutJob is a single clips-to-extract entry in a CutRequest batch.
type CutJob struct {
	StartSec   float64
	EndSec     float64
	OutputPath string
}

// CutItemStatus enumerates the per-job outcome of a Cut step.
// Aggregating on the status enum (rather than string-matching Err
// patterns) lets dashboards partition success/failure/validation
// outcomes without unwrapping chains.
type CutItemStatus int

const (
	// CutItemStatusUnknown is the zero-value; never written by the
	// canonical FFmpegCutter. A sanity-check logger fails fast on
	// Items with status Unknown at the orchestrator boundary.
	CutItemStatusUnknown CutItemStatus = iota
	// CutItemStatusSucceeded means ffmpeg exited 0 and an output
	// file exists at OutputPath. Status, SizeBytes are populated;
	// DurationSec is 0 (probe not yet run); Err is nil.
	CutItemStatusSucceeded
	// CutItemStatusFailed means ffmpeg exited non-zero and no
	// output file exists at OutputPath. Err carries the failure
	// reason; SizeBytes, DurationSec are zero values.
	CutItemStatusFailed
	// CutItemStatusValidated marks a previously-Succeeded item
	// whose ffprobe-validation step ran without error
	// (DurationSec is populated from the ffprobe report).
	CutItemStatusValidated
	// CutItemStatusProbeFailed (FASE 2.4): file is on disk and
	// downstream rendering can consume it, but the ffprobe
	// validation step did not produce a parseable MediaInfo.
	// Err carries the wrapped probe failure; SizeBytes > 0;
	// DurationSec is 0 (could not be determined). Soft-fail for
	// production (don't drop a perfectly good clip because the
	// probe subprocess died); surfaced as Err so dashboards can
	// flag "playable but unvalidated" entries for re-validation.
	CutItemStatusProbeFailed
)

// String returns the canonical human-readable label for a
// CutItemStatus (used by tests / dashboards / log aggregation).
func (s CutItemStatus) String() string {
	switch s {
	case CutItemStatusSucceeded:
		return "succeeded"
	case CutItemStatusFailed:
		return "failed"
	case CutItemStatusValidated:
		return "validated"
	case CutItemStatusProbeFailed:
		return "probe_failed"
	default:
		return "unknown"
	}
}

// CutItemResult is the per-job typed result for partial-success
// tracking (Blocco 4 + FASE 2.4 — July 2026, audit P0 #4
// continuation). When the cutter reports partial failure (some
// clips produced, some failed), CutItemResult carries the outcome
// of each individual job so the application layer can attribute
// failures (and ffprobe-validation outcome) per-job without
// guessing which JobID failed.
//
// Status invariants (FASE 2.4 — wire-format contract):
//
//	CutItemStatusSucceeded   ⇒ Err == nil, SizeBytes > 0,
//	                            DurationSec == 0 (probe not yet run)
//	CutItemStatusValidated   ⇒ Err == nil, SizeBytes > 0,
//	                            DurationSec > 0 (probe succeeded)
//	CutItemStatusProbeFailed ⇒ Err != nil (wrapped probe error),
//	                            SizeBytes > 0 (file IS on disk),
//	                            DurationSec == 0 (could not be
//	                            determined). Soft-fail for downstream
//	                            render — the file is playable.
//	CutItemStatusFailed      ⇒ Err != nil, OutputPath empty,
//	                            SizeBytes / DurationSec == 0.
//
// Helper accessors (SuccessfulItems / FailedItems / AllSucceeded
// / Clip.Succeeded) treat Succeeded | Validated | ProbeFailed as
// "file-on-disk-playable" (SuccessfulItems; Clip.Succeeded()) and
// Failed / Unknown as "non-playable" (FailedItems; AllSucceeded
// predicate stays strict: any non-{Succeeded,Validated} = NOT
// all-succeeded even if the file is on disk).
type CutItemResult struct {
	// JobID is the OutputPath from the original CutJob — stable
	// per (SourcePath, batch, index), used for caller-side dedupe
	// regardless of cut outcome.
	JobID string

	// OutputPath is the absolute path the adapter wrote the clip
	// to. Empty when Status == CutItemStatusFailed.
	OutputPath string

	// Status is the typed outcome enum.
	Status CutItemStatus

	// SizeBytes is the on-disk size of the produced clip in bytes;
	// 0 when Status == CutItemStatusFailed.
	SizeBytes int64

	// DurationSec is the ffprobe-reported duration of the
	// produced clip in seconds. 0 when ffprobe did not run,
	// Status == CutItemStatusProbeFailed (could not be
	// determined), or Status == CutItemStatusFailed.
	DurationSec float64

	// Err is the per-job failure reason; nil on success
	// (Succeeded / Validated). Non-nil for ProbeFailed (wrapped
	// probe error) and Failed (per-clip cut error).
	Err error
}

// CutBatchResult is the structured, non-nil batch result returned
// by VideoCutter.Cut (FASE 2.4 — July 2026, audit P0 #4
// continuation). The struct lifts the previous "ProducedPaths only"
// contract so callers receive:
//
//   - Per-job outcome Items in input-Jobs order — len(Items) ==
//     len(input.Jobs) ALWAYS, even on full failure (no zero-output
//     nil results).
//   - SourcePath for audit traceability.
//
// "mai nil con zero output" invariant: the contract populates every
// Item that did not produce a file with JobID + Status=Failed +
// Err, so callers MUST be able to iterate Items without nil-checks
// or "what if Items is shorter than Jobs" guards.
//
// Helper accessors keep the typing ergonomic:
//
//	ProducedPaths()    → []string (Succeeded|Validated|ProbeFailed)
//	SuccessfulItems()  → []CutItemResult (file-on-disk-playable)
//	FailedItems()      → []CutItemResult (Status == Failed)
//	AllSucceeded()     → bool (strict equality, no ProbeFailed)
type CutBatchResult struct {
	// SourcePath is the input video path the batch was cut from.
	// Stable per call — audit-friendly.
	SourcePath string

	// Items is the per-job outcome in input-Jobs order.
	// len(Items) == len(input.Jobs) is GUARANTEED by the
	// VideoCutter.Cut contract; a partial-success result carries
	// the produced items in Items alongside the failed items with
	// Status=CutItemStatusFailed.
	Items []CutItemResult
}

// ProducedPaths returns the produced output paths for items in
// Status ∈ {Succeeded, Validated, ProbeFailed} — every Item whose
// file is on disk. Equivalent to the pre-FASE-2.4
// CutResult.ProducedPaths surface, retained for callers that only
// need the path list (ProbeFailed is included because the file is
// playable downstream even when ffprobe could not parse it).
func (b CutBatchResult) ProducedPaths() []string {
	out := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		if it.OutputPath == "" {
			continue
		}
		switch it.Status {
		case CutItemStatusSucceeded, CutItemStatusValidated, CutItemStatusProbeFailed:
			out = append(out, it.OutputPath)
		}
	}
	return out
}

// SuccessfulItems returns the Items filtered to Status ∈
// {Succeeded, Validated, ProbeFailed} — every "file-on-disk-
// playable" outcome. Callers that need a strict
// "ffprobe-validated-only" partition should filter on
// Status == CutItemStatusValidated directly.
func (b CutBatchResult) SuccessfulItems() []CutItemResult {
	out := make([]CutItemResult, 0, len(b.Items))
	for _, it := range b.Items {
		switch it.Status {
		case CutItemStatusSucceeded, CutItemStatusValidated, CutItemStatusProbeFailed:
			out = append(out, it)
		}
	}
	return out
}

// FailedItems returns the Items filtered to Status == Failed.
// Items in CutItemStatusProbeFailed are NOT FailedItems — the file
// IS on disk and downstream rendering can use it; probe-failure is
// surfaced via Item.Err on the SuccessfulItems side.
func (b CutBatchResult) FailedItems() []CutItemResult {
	out := make([]CutItemResult, 0, len(b.Items))
	for _, it := range b.Items {
		if it.Status == CutItemStatusFailed {
			out = append(out, it)
		}
	}
	return out
}

// AllSucceeded returns true when every Item is in StrictSucceeded
// (Succeeded + Validated). ProbeFailed / Failed / Unknown return
// false even when the file is on disk — the predicate is strict by
// design so dashboards partition "fully validated" from "soft-fail".
func (b CutBatchResult) AllSucceeded() bool {
	for _, it := range b.Items {
		if it.Status != CutItemStatusSucceeded && it.Status != CutItemStatusValidated {
			return false
		}
	}
	return true
}

// ── Clip DTO (parallel-array replacement, FASE 2.4 — July 2026) ─────

// Clip is the structured per-clip DTO used by processSingleVideo
// and InterleaveClips after FASE 2.4 removed the previous
// ([]string paths, []string titles, []string sourceIDs) parallel-
// array shape. Consolidating the three parallel slices into one
// Clip value per produced clip eliminates the producedSet / index
// alignment logic the previous code paths relied on.
//
// The struct lives in the application layer alongside the existing
// CutResult / CutJob / CutBatchResult DTOs so all per-clip
// metadata is co-located in render_ports.go (single owner per clip
// fact per AGENTS.md godlike/06).
type Clip struct {
	// Path is the absolute path of the produced clip on disk.
	// Empty when Status == CutItemStatusFailed.
	Path string

	// Title is the human-readable clip title (the
	// "<vs.Title>_<idx>" pattern processSingleVideo emits).
	Title string

	// SourceID is the canonical source video identifier (the
	// yt-dlp video ID for stockpipeline; the YouTube video ID
	// for the YouTube cutover path). Stays unit-test friendly
	// without driving on Path filesystem state.
	SourceID string

	// Status carries the underlying CutItemStatus so callers can
	// route failed clips into the canonical "downgrade to
	// soft-skip" path without re-deriving from Path-presence.
	Status CutItemStatus

	// SizeBytes is the on-disk size; 0 when Status == Failed.
	SizeBytes int64

	// DurationSec is the ffprobe-reported duration; 0 when
	// ffprobe did not run, Status == ProbeFailed (could not be
	// determined), or Status == Failed.
	DurationSec float64

	// Err is the per-clip failure reason; nil on Succeeded /
	// Validated. Non-nil on ProbeFailed (wrapped probe error).
	Err error
}

// Succeeded returns true when the clip's Status is in
// {Succeeded, Validated, ProbeFailed} — the "file-on-disk-
// playable" predicate. Source-fed consumers (InterleaveClips,
// renderChunk) skip non-Succeeded clips at iteration time via
// this predicate; Failed clips are dropped silently.
func (c Clip) Succeeded() bool {
	return c.Status == CutItemStatusSucceeded ||
		c.Status == CutItemStatusValidated ||
		c.Status == CutItemStatusProbeFailed
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

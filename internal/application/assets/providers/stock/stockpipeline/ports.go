// Package stock — StockRenderer port + canonical RenderRequest DTOs (PR6, June 2026).
//
// Per PR6 spec (Pattern 0 + Pattern 8): the application layer decides WHICH
// clips receive transitions/effects and WHAT the encoding policy is.
// It does NOT know how FFmpeg builds the filter_complex, runs the binary, or
// assembles the codec args — all that lives in the infrastructure layer
// behind the canonical `StockRenderer` port.
//
// Import-boundary invariant (verified by `go vet`):
//
//	go vet ./internal/application/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/infrastructure/process`. Both are infra concerns; the app layer
// only depends on the StockRenderer port and the TransitionRegistry
// catalog defined here.
package stockpipeline

import (
	"context"

	"go.uber.org/zap"
)

// ── Port ────────────────────────────────────────────────────────────────

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

// ── DTOs (application-layer neutral types) ──────────────────────────────

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

// ── Cutter Port (PR6) ─────────────────────────────────────────────────

// VideoCutter extracts multiple clips from a single source video. The
// port encapsulates the batch-vs-fallback-to-individual branching and
// the on-disk verification: callers receive exactly the list of
// `OutputPath`s that were written. Implementations live in
// `internal/infrastructure/media/render/`.
type VideoCutter interface {
	// Cut extracts N clips from a single source video. The batch of
	// jobs shares the same SourcePath; encoding policy (codec / preset
	// / crf / audio) is uniform across the batch.
	//
	// Returned CutResult.ProducedPaths lists outputs that actually
	// exist on disk after the (possibly multi-attempt) cut. On a full
	// failure, ProducedPaths is empty and a non-nil error is returned
	// capturing the LAST underlying failure (the adapter logs the
	// rest internally).
	Cut(ctx context.Context, req CutRequest) (CutResult, error)
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

// CutResult is the neutral application-layer result the application
// receives from VideoCutter.Cut. ProducedPaths lists each OutputPath
// for which a file was successfully written; the caller's batch loop
// just reads this slice — no os.Stat checks needed in app code.
type CutResult struct {
	// ProducedPaths is the OUTPUTS that exist on disk after Cut
	// returned. Empty when every job failed.
	ProducedPaths []string
}

// CutItemResult is the per-job typed result for partial-success
// tracking (Blocco 4, July 2026 — audit P0 #4). When the cutter
// reports partial failure (some clips produced, some failed),
// CutItemResult carries the outcome of each individual job so the
// application layer can align clipTitles with ProducedPaths
// without guessing which job failed.
//
// Forward-declaration: the VideoCutter.Cut port still returns
// (CutResult, error); CutItemResult is the target shape for a
// future port cutover where Cut returns []CutItemResult. Until
// then, processSingleVideo aligns titles via the producedSet map
// (see process.go Blocco 4).
type CutItemResult struct {
	JobID      string // OutputPath from the original CutJob
	OutputPath string // same as JobID when successful, empty on failure
	Err        error  // nil on success, the per-job failure reason otherwise
}

// ── Compile-time anchors ────────────────────────────────────────────────

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

func (noOpCutter) Cut(ctx context.Context, req CutRequest) (CutResult, error) {
	return CutResult{}, nil
}

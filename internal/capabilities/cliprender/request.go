// Package cliprender owns the canonical clip.render capability: the typed
// request contract, its fail-fast validation, the HTTP transport and the
// worker that turns a validated request into a published derived asset.
//
// The transport lives in this package too (handler.go, handler_batch.go and
// handler_sequence.go for POST /clips/render, /render/batch and
// /render/sequence); everything below the transport — ClipRenderPlanV1
// compilation, transcript/subtitle resolution, the RenderingGen/Chronon
// render and the derived-asset commit — runs in worker.go and its siblings.
//
// Architecture contract (AGENTS.md): this is a NEW capability — it does
// not extend /api/clips/process (ingest/extraction) nor /api/clips/stock
// (stock acquisition). It reuses the canonical Master job queue, the
// canonical asset registry, and the existing Rust execution boundary:
// no second renderer service, no second queue.
package cliprender

import (
	"errors"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TypeClipRender is the canonical job type for the clip post-processing
// pipeline. Jobs enqueue on the same Master queue as every other
// capability; the worker consumes the fully-resolved request (no
// business decisions inside Rust). The literal is owned by the kernel
// (shared job-type identity); this package re-exports it so capability
// consumers reference one stable identifier.
const TypeClipRender = job.TypeClipRender

const (
	SchedulingResourceClass = "render"
	ExecutionLaneSubmit     = "submit"
	ExecutionLaneSettle     = "settle"
)

// DefaultDriveRootFolderID is the canonical certification TMP root.
// Every validated render artifact is uploaded under
// DefaultDriveRootFolderID/<run-id>/ — never directly in the root.
// `destination.drive_folder_id` is an explicit caller override only.
const DefaultDriveRootFolderID = "1Ay0swz9xkwPoJErvpE_qkYowHCf1OSwC"

// ── Enum literals (canonical, single owner: this package) ────────────

const (
	// Background modes.
	BackgroundModeNone       = "none"
	BackgroundModeBlurSource = "blur_source"
	BackgroundModeAsset      = "asset"

	// Background asset KINDS live in background.go — the single owner of the
	// media-family decision.

	// Transcript policies. `auto` is the default: reuse a READY canonical
	// text track, and generate + persist one only when the lookup misses
	// (intro clips often have no track yet). `reuse` is the strict,
	// deterministic lane: no ASR, missing READY fails closed. `generate`
	// forces a fresh ASR pass and skips the lookup.
	//
	// The old `reuse_or_generate` name was retired; `auto` is the explicit
	// policy and its fallback is bounded by the same ASR path/timeout.
	TranscriptModeAuto     = "auto"
	TranscriptModeReuse    = "reuse"
	TranscriptModeGenerate = "generate"

	// Subtitle modes.
	SubtitlesModeBurn    = "burn"
	SubtitlesModeSidecar = "sidecar"

	// Audio copy policies.
	AudioModeCopyIfCompatible = "copy_if_compatible"
	// AudioModeTranscode is retained in the wire vocabulary but is not
	// accepted: the native mux copies the source stream and has no transcoder,
	// so requests and sealed plans carrying it are rejected at the boundary.
	AudioModeTranscode = "transcode"

	// The output media contract identities live in output_contracts.go (they
	// re-export kernel/media and stay out of this file's line budget).

	// Deprecated compatibility aliases; defaults are sourced from the kernel
	// contract by the resolver and normalization paths.
	DefaultWidth  = 1920
	DefaultHeight = 1080
	DefaultFPSNum = 24
	DefaultFPSDen = 1

	// Watermark positions.
	PositionTopLeft     = "top_left"
	PositionTopRight    = "top_right"
	PositionCenter      = "center"
	PositionBottomLeft  = "bottom_left"
	PositionBottomRight = "bottom_right"

	// Output defaults for the horizontal YouTube use case. Framerate is a
	// rational num/den pair so NTSC rates (30000/1001 = 29.97) survive the
	// contract exactly instead of being rounded/forced to an integer.
	DefaultLanguage = "en"

	// Bounds.
	MinDimension = 16
	MaxDimension = 7680
	MinFPS       = 1
	MaxFPS       = 120
)

// ErrInvalidRequest is the typed sentinel wrapping every validation
// failure. Callers use errors.Is for classification; the message
// carries the human-readable reason.
var ErrInvalidRequest = errors.New("invalid clip.render request")

// ── Request spec (nested option blocks) ───────────────────────────────

// RenderRequest is the canonical clip.render wire payload. It is both
// the HTTP body of POST /api/clips/render and the job payload persisted
// by the Master queue — one shape, no drift between transport and
// worker.
type RenderRequest struct {
	SourceAssetID string `json:"source_asset_id"`
	// SourceRevision is the optional content revision (SHA-256 hex) of the
	// source bytes this render was requested against. It enters the render
	// fingerprint, so changing the revision is a different cache identity.
	// When omitted, the source is addressed by ID only: that is the historical
	// contract and it assumes the asset is immutable per ID. A pinned revision
	// that does not match the resolved bytes fails closed in preparation
	// (ErrInputRevisionMismatch). See fingerprint.go for the rule.
	SourceRevision string           `json:"source_revision,omitempty"`
	Background     *BackgroundSpec  `json:"background,omitempty"`
	Watermark      *WatermarkSpec   `json:"watermark,omitempty"`
	Transcript     *TranscriptSpec  `json:"transcript,omitempty"`
	Subtitles      *SubtitlesSpec   `json:"subtitles,omitempty"`
	Output         *OutputSpec      `json:"output,omitempty"`
	Audio          *AudioSpec       `json:"audio,omitempty"`
	Destination    *DestinationSpec `json:"destination,omitempty"`
	Execution      *ExecutionSpec   `json:"execution,omitempty"`
	// Overlays is the set of entity-overlay lineages this final video
	// composites: ONE lineage per certified overlay.render artifact. Empty for
	// subtitles/watermark-only clips. Production renders one short video per
	// semantic overlay item, so a clip compositing a phrase AND an entity card
	// declares two lineages (a single slot silently dropped the rest).
	Overlays []OverlayRefSpec `json:"overlays,omitempty"`
}

// Normalize applies the canonical defaults. It is idempotent and
// mutating; call it before Validate and before persisting the payload.
func (r *RenderRequest) Normalize() {
	if r.Background == nil {
		r.Background = &BackgroundSpec{}
	}
	if r.Background.Mode == "" {
		r.Background.Mode = BackgroundModeNone
	}
	if r.Watermark == nil {
		r.Watermark = &WatermarkSpec{}
	}
	if r.Watermark.Position == "" {
		r.Watermark.Position = PositionTopRight
	}
	// An explicit opacity 0 means "invisible" and an explicit margin 0 means
	// "flush to the edge": both are honoured. Only an ABSENT key falls back to
	// the default (a Go-constructed literal with a zero value counts as absent,
	// which preserves the historical default for every non-JSON caller).
	if r.Watermark.Opacity == 0 && !r.Watermark.opacitySet {
		r.Watermark.Opacity = 1.0
		// The default applied here is now explicit, not implied by a zero
		// value, so the persisted payload carries it verbatim.
		r.Watermark.opacitySet = true
	}
	// Watermark defaults are owned SOLELY by Normalize (single owner). The
	// downstream mapper/plan serialise verbatim without re-defaulting.
	if r.Watermark.Enabled && r.Watermark.MarginPX == 0 && !r.Watermark.marginSet {
		if strings.TrimSpace(r.Watermark.Text) != "" {
			r.Watermark.MarginPX = 100
		} else {
			r.Watermark.MarginPX = 40
		}
	}
	if r.Watermark.Enabled && strings.TrimSpace(r.Watermark.Text) != "" {
		if r.Watermark.Style == nil {
			r.Watermark.Style = defaultOverlayTextStyle(34, 3, 5)
		}
	}
	if r.Transcript == nil {
		r.Transcript = &TranscriptSpec{}
	}
	if r.Transcript.Mode == "" {
		r.Transcript.Mode = TranscriptModeAuto
	}
	if r.Transcript.Language == "" {
		r.Transcript.Language = DefaultLanguage
	}
	// Persist defaults to TRUE: an explicit `generate` repair without
	// persistence never populates the canonical text track, so every clip of a
	// batch would pay its own ASR pass for the same source. Only an explicit
	// `"persist": false` (a decoded key, or a Go literal that set persistSet)
	// opts out. Setting persistSet here keeps Normalize idempotent.
	if !r.Transcript.persistSet {
		r.Transcript.Persist = true
		r.Transcript.persistSet = true
	}
	if r.Subtitles == nil {
		r.Subtitles = &SubtitlesSpec{}
	}
	if r.Subtitles.Mode == "" {
		r.Subtitles.Mode = SubtitlesModeBurn
	}
	if r.Subtitles.Style != nil && r.Subtitles.Style.FontSizePX == 0 && r.Subtitles.Style.Size > 0 {
		r.Subtitles.Style.FontSizePX = r.Subtitles.Style.Size
	}
	if r.Subtitles.Enabled && r.Subtitles.Style == nil {
		// Do not leave the default white subtitle glyphs unoutlined: on bright
		// source frames they become unreadable and the intended shadow is lost.
		// 58 px matches the shorts-v1 ASS preset (ResolveFontPreset) so the
		// burned layer and the sidecar agree, and the keyline is ~5% of the em
		// rather than a fixed 5 px that merged adjacent glyph contours.
		r.Subtitles.Style = defaultOverlayTextStyle(58, 3, 6)
	}
	if r.Output == nil {
		r.Output = &OutputSpec{}
	}
	if r.Output.Contract == "" {
		r.Output.Contract = OutputContractVeloxAssemblyReadyV1
	}
	if r.Output.Width == 0 {
		r.Output.Width = 1920
	}
	if r.Output.Height == 0 {
		r.Output.Height = 1080
	}
	if r.Output.FPSNum == 0 {
		r.Output.FPSNum = 24
		r.Output.FPSDen = 1
	} else if r.Output.FPSDen == 0 {
		r.Output.FPSDen = 1
	}
	// 0 is the wire-level "use the contract default" spelling. Promote it to
	// the actual default HERE so the persisted payload, the sealed plan and the
	// render fingerprint all agree: 0 and 100 describe the same pixels and must
	// not address two different cache entries (CR-01).
	if r.Output.ForegroundScalePercent == 0 {
		r.Output.ForegroundScalePercent = 100
	}
	if r.Audio == nil {
		r.Audio = &AudioSpec{}
	}
	if r.Audio.Mode == "" {
		r.Audio.Mode = AudioModeCopyIfCompatible
	}
	if r.Destination == nil {
		r.Destination = &DestinationSpec{}
	}
	if r.Destination.DriveFolderID == "" {
		r.Destination.DriveFolderID = DefaultDriveRootFolderID
	}
	if r.Execution == nil {
		r.Execution = &ExecutionSpec{}
	}
}

// Validate performs the fail-fast contract gate. Call Normalize first —
// zero values are interpreted per the canonical defaults, so a request
// that was never normalized is validated against those same defaults.

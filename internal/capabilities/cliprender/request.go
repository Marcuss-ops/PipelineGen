// Package cliprender owns the canonical clip.render capability: the
// typed request contract, its fail-fast validation, and the job-type
// identity for the async Master job. The HTTP transport lives in
// internal/capabilities/assets/cliprender; the render worker (ClipRenderPlanV1
// compilation, transcript/subtitle resolution, RenderingGen/Chronon render,
// validation, Drive upload, derived-asset commit) lands in follow-up
// steps and stays inside this package.
//
// Architecture contract (AGENTS.md): this is a NEW capability — it does
// not extend /api/clips/process (ingest/extraction) nor /api/clips/stock
// (stock acquisition). It reuses the canonical Master job queue, the
// canonical asset registry, and the existing Rust execution boundary:
// no second renderer service, no second queue.
package cliprender

import (
	"errors"
	"fmt"
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

	// Transcript policies. clip.render is a RENDER step: the canonical
	// transcript must already exist, so `reuse` is the default and a missing
	// READY track FAILS CLOSED. `generate` remains only as an explicit manual
	// repair request; there is no implicit ASR inside a render.
	//
	// The legacy `reuse_or_generate` mode was DELETED in the 2026-09-13
	// clip.render audit (P1): an ASR miss could silently turn a 5s render into
	// a 20-40s one, which made the render path non-deterministic.
	TranscriptModeReuse    = "reuse"
	TranscriptModeGenerate = "generate"

	// Subtitle modes.
	SubtitlesModeBurn    = "burn"
	SubtitlesModeSidecar = "sidecar"

	// Audio copy policies.
	AudioModeCopyIfCompatible = "copy_if_compatible"
	AudioModeTranscode        = "transcode"

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

// BackgroundSpec selects the rendered background. mode "" (omitted) is
// normalised to BackgroundModeNone.
//
// Kind is the media family of the background ASSET (see background.go). It is
// optional: empty means "derive it from the resolved asset's canonical
// MediaType"; an explicit value wins. Kind is rejected for mode=none/
// RenderRequest is the canonical clip.render wire payload. It is both
// the HTTP body of POST /api/clips/render and the job payload persisted
// by the Master queue — one shape, no drift between transport and
// worker.
type RenderRequest struct {
	SourceAssetID string           `json:"source_asset_id"`
	Background    *BackgroundSpec  `json:"background,omitempty"`
	Watermark     *WatermarkSpec   `json:"watermark,omitempty"`
	Transcript    *TranscriptSpec  `json:"transcript,omitempty"`
	Subtitles     *SubtitlesSpec   `json:"subtitles,omitempty"`
	Output        *OutputSpec      `json:"output,omitempty"`
	Audio         *AudioSpec       `json:"audio,omitempty"`
	Destination   *DestinationSpec `json:"destination,omitempty"`
	Execution     *ExecutionSpec   `json:"execution,omitempty"`
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
		r.Transcript.Mode = TranscriptModeReuse
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
func (r *RenderRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	if r.SourceAssetID == "" {
		return fmt.Errorf("%w: source_asset_id is required (a canonical clip asset)", ErrInvalidRequest)
	}
	// Normalize() is the single owner of the nested-block defaults, so every
	// nested block it materializes must exist before this gate reads it.
	// Failing closed with a typed error is the contract: dereferencing nil here
	// used to panic, which no caller can handle.
	if r.Background == nil || r.Watermark == nil || r.Transcript == nil ||
		r.Subtitles == nil || r.Output == nil || r.Audio == nil ||
		r.Destination == nil {
		return fmt.Errorf("%w: request is not normalized (call Normalize before Validate)", ErrInvalidRequest)
	}

	// execution.require_zero_copy is UNSATISFIABLE: after the PATH B CUDA
	// hybrid removal no backend certifies video_zero_copy, so a request that
	// sets it can never succeed. It is rejected HERE — at the request
	// boundary — before an enqueue, an asset download or a GPU render is ever
	// spent on it. The historical gate sat in the completion path, AFTER
	// Chronon had rendered the whole clip: a full GPU render burned to fail a
	// request that was already doomed at decode time.
	if r.Execution != nil && r.Execution.RequireZeroCopy {
		return fmt.Errorf("%w: execution.require_zero_copy=true is unsatisfiable: no backend certifies video_zero_copy (GPU compositing is Chronon-only via the RenderingGen queue)", ErrInvalidRequest)
	}

	switch r.Background.Mode {
	case BackgroundModeNone, BackgroundModeBlurSource:
		// No asset and therefore no media family: a declared kind would be an
		// unresolved contradiction (which plate? the mode says there is none).
		if r.Background.Kind != "" {
			return fmt.Errorf("%w: background.kind must be empty for mode=%s (the mode carries no asset)", ErrInvalidRequest, r.Background.Mode)
		}
	case BackgroundModeAsset:
		if r.Background.AssetID == "" {
			return fmt.Errorf("%w: background.mode=asset requires background.asset_id", ErrInvalidRequest)
		}
		if r.Background.Kind != "" && !IsBackgroundKind(r.Background.Kind) {
			return fmt.Errorf("%w: background.kind must be one of %s, %s (got %q)", ErrInvalidRequest, BackgroundKindImage, BackgroundKindVideo, r.Background.Kind)
		}
	default:
		return fmt.Errorf("%w: background.mode must be one of none, blur_source, asset (got %q)", ErrInvalidRequest, r.Background.Mode)
	}

	if r.Watermark.Enabled {
		if strings.TrimSpace(r.Watermark.AssetID) == "" && strings.TrimSpace(r.Watermark.Text) == "" {
			return fmt.Errorf("%w: watermark.enabled=true requires watermark.asset_id or watermark.text", ErrInvalidRequest)
		}
		switch r.Watermark.Position {
		case PositionTopLeft, PositionTopRight, PositionCenter, PositionBottomLeft, PositionBottomRight:
		default:
			return fmt.Errorf("%w: watermark.position must be one of top_left, top_right, center, bottom_left, bottom_right (got %q)", ErrInvalidRequest, r.Watermark.Position)
		}
		if r.Watermark.Opacity < 0 || r.Watermark.Opacity > 1 {
			return fmt.Errorf("%w: watermark.opacity must be within [0,1] (got %v)", ErrInvalidRequest, r.Watermark.Opacity)
		}
		if r.Watermark.MarginPX < 0 {
			return fmt.Errorf("%w: watermark.margin_px must be >= 0 (got %d)", ErrInvalidRequest, r.Watermark.MarginPX)
		}
	}

	switch r.Transcript.Mode {
	case TranscriptModeReuse, TranscriptModeGenerate:
	default:
		return fmt.Errorf("%w: transcript.mode must be one of reuse, generate (got %q)", ErrInvalidRequest, r.Transcript.Mode)
	}
	if r.Transcript.Language == "" {
		return fmt.Errorf("%w: transcript.language is required (default en)", ErrInvalidRequest)
	}

	if r.Subtitles.Enabled {
		switch r.Subtitles.Mode {
		case SubtitlesModeBurn, SubtitlesModeSidecar:
		default:
			return fmt.Errorf("%w: subtitles.mode must be one of burn, sidecar (got %q)", ErrInvalidRequest, r.Subtitles.Mode)
		}
	}

	if r.Output.Contract == "" {
		return fmt.Errorf("%w: output.contract is required (default VELOX_ASSEMBLY_READY_V1)", ErrInvalidRequest)
	}
	if r.Output.Width < MinDimension || r.Output.Width > MaxDimension {
		return fmt.Errorf("%w: output.width must be within [%d,%d] (got %d)", ErrInvalidRequest, MinDimension, MaxDimension, r.Output.Width)
	}
	if r.Output.Height < MinDimension || r.Output.Height > MaxDimension {
		return fmt.Errorf("%w: output.height must be within [%d,%d] (got %d)", ErrInvalidRequest, MinDimension, MaxDimension, r.Output.Height)
	}
	if r.Output.Width <= r.Output.Height {
		return fmt.Errorf("%w: vertical output is disabled; YouTube clips must be horizontal (width %d must be greater than height %d)", ErrInvalidRequest, r.Output.Width, r.Output.Height)
	}
	if r.Output.FPSNum <= 0 || r.Output.FPSDen <= 0 {
		return fmt.Errorf("%w: output.fps_num/fps_den must be positive (got %d/%d)", ErrInvalidRequest, r.Output.FPSNum, r.Output.FPSDen)
	}
	fps := float64(r.Output.FPSNum) / float64(r.Output.FPSDen)
	if fps < MinFPS || fps > MaxFPS {
		return fmt.Errorf("%w: output.fps must be within [%d,%d] (got %d/%d = %.3f)", ErrInvalidRequest, MinFPS, MaxFPS, r.Output.FPSNum, r.Output.FPSDen, fps)
	}
	if r.Output.ForegroundScalePercent < 0 || r.Output.ForegroundScalePercent > 100 {
		return fmt.Errorf("%w: output.foreground_scale_percent must be within [1,100] when set (got %d)", ErrInvalidRequest, r.Output.ForegroundScalePercent)
	}
	// One validator for the card block, shared with the sealed plan, so the
	// request and the plan cannot accept different source_frame shapes.
	if err := ValidatePlanSourceFrame(r.Output.SourceFrame, r.Output.ForegroundScalePercent); err != nil {
		return fmt.Errorf("%w: output.%w", ErrInvalidRequest, err)
	}
	if err := ValidateClipEffects(r.Output.Effects, r.Output.Grid); err != nil {
		return fmt.Errorf("%w: output.%w", ErrInvalidRequest, err)
	}

	switch r.Audio.Mode {
	case AudioModeCopyIfCompatible, AudioModeTranscode:
	default:
		return fmt.Errorf("%w: audio.mode must be one of copy_if_compatible, transcode (got %q)", ErrInvalidRequest, r.Audio.Mode)
	}

	if r.Destination.DriveFolderID == "" {
		return fmt.Errorf("%w: destination.drive_folder_id is required (default DefaultDriveRootFolderID)", ErrInvalidRequest)
	}

	// Overlay lineages are all-or-nothing, EACH of them: a final video that
	// declares overlays must carry the complete chain (render job id + plan
	// fingerprint + render key + source video asset id + the declared
	// compositing window) for every one, so the composition is provable, never
	// a bare "an overlay was here" and never an untimed blend. A partial
	// lineage is rejected rather than dropped: silently compositing three of
	// four declared overlays is the failure this list contract exists to stop.
	for i, overlay := range r.Overlays {
		if strings.TrimSpace(overlay.RenderJobID) == "" ||
			strings.TrimSpace(overlay.PlanFingerprint) == "" ||
			strings.TrimSpace(overlay.RenderKey) == "" ||
			strings.TrimSpace(overlay.SourceVideoAssetID) == "" {
			return fmt.Errorf("%w: overlay %d requires render_job_id, plan_fingerprint, render_key and source_video_asset_id", ErrInvalidRequest, i)
		}
		if overlay.StartUS < 0 || overlay.EndUS <= overlay.StartUS {
			return fmt.Errorf("%w: overlay %d requires a valid compositing window (end_us > start_us >= 0)", ErrInvalidRequest, i)
		}
	}

	return nil
}

// Package cliprender owns the canonical clip.render capability: the
// typed request contract, its fail-fast validation, and the job-type
// identity for the async Master job. The HTTP transport lives in
// internal/api/assets/cliprender; the render worker (ClipRenderPlanV1
// compilation, transcript/subtitle resolution, Rust single-pass render,
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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

	// Transcript policies.
	TranscriptModeReuse           = "reuse"
	TranscriptModeGenerate        = "generate"
	TranscriptModeReuseOrGenerate = "reuse_or_generate"

	// Subtitle modes.
	SubtitlesModeBurn    = "burn"
	SubtitlesModeSidecar = "sidecar"

	// Audio copy policies.
	AudioModeCopyIfCompatible = "copy_if_compatible"
	AudioModeTranscode        = "transcode"

	// Output media contracts are owned by kernel/media; these aliases keep the
	// capability wire surface source-compatible.
	OutputContractVeloxAssemblyReadyV1 = "VELOX_ASSEMBLY_READY_V1"
	OutputContractVeloxAssemblyReadyV2 = "VELOX_ASSEMBLY_READY_V2"
	OutputContractVeloxEditingClipV1   = "velox-editing-clip-v1"

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
type BackgroundSpec struct {
	Mode    string `json:"mode,omitempty"`
	AssetID string `json:"asset_id,omitempty"` // required when mode == "asset"
}

// WatermarkSpec overlays a canonical watermark asset. When disabled the
// block may be omitted entirely.
type WatermarkSpec struct {
	Enabled  bool    `json:"enabled,omitempty"`
	Text     string  `json:"text,omitempty"`
	AssetID  string  `json:"asset_id,omitempty"`  // required when enabled
	Position string  `json:"position,omitempty"`  // default top_right
	Opacity  float64 `json:"opacity,omitempty"`   // 0.0–1.0, default 1.0
	MarginPX int     `json:"margin_px,omitempty"` // >= 0, default 0
	// Style is the canonical visual override block (size, color, shadow,
	// transition). It is the kernel/script SSOT definition — this boundary
	// projects it verbatim, never re-defines it.
	Style *scriptpkg.VideoVisualStyleSpec `json:"style,omitempty"`
}

// TranscriptSpec controls canonical transcript resolution. The worker
// reuses the canonical text track when it already exists; generation
// persists the transcript once into the DB (never a temp WAV).
type TranscriptSpec struct {
	Mode     string `json:"mode,omitempty"` // default reuse_or_generate
	Language string `json:"language,omitempty"`
	Persist  bool   `json:"persist,omitempty"`
}

// SubtitlesSpec compiles a deterministic .ass from the canonical
// transcript timing — never re-runs speech recognition for subtitles.
type SubtitlesSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"` // default burn
	StyleID string `json:"style_id,omitempty"`
	// Style is the canonical visual override block (color, size, shadow,
	// transition). It is the kernel/script SSOT definition — this boundary
	// projects it verbatim, never re-defines it.
	Style *scriptpkg.VideoVisualStyleSpec `json:"style,omitempty"`
}

// OutputSpec pins the VeloxEditing-compatible output contract. The
// precise codec/pixel/timebase values are owned by the contract
// (follow-up step); the request only selects it + resolution/fps.
type OutputSpec struct {
	Contract string `json:"contract,omitempty"` // default VELOX_ASSEMBLY_READY_V1
	Width    int    `json:"width,omitempty"`    // default 1920 (YouTube horizontal)
	Height   int    `json:"height,omitempty"`   // default 1080 (YouTube horizontal)
	FPSNum   int    `json:"fps_num,omitempty"`  // default 24
	FPSDen   int    `json:"fps_den,omitempty"`  // default 1
}

// AudioSpec selects the audio copy policy. copy_if_compatible never
// re-encodes audio that already satisfies the target contract.
type AudioSpec struct {
	Mode string `json:"mode,omitempty"` // default copy_if_compatible
}

// DestinationSpec routes the validated artifacts on Drive.
type DestinationSpec struct {
	DriveFolderID string `json:"drive_folder_id,omitempty"` // default DefaultDriveRootFolderID
}

// ExecutionSpec selects the render execution policy. It is the ONLY
// request-level backend signal: the concrete backend is resolved by the
// RenderBackendResolver from probed host capabilities, never hardcoded here.
type ExecutionSpec struct {
	// RequireGPU fails the render unless the resolved backend is any registered
	// GPU backend (CUDA or Chronon Vulkan). Default false allows software.
	RequireGPU bool `json:"require_gpu,omitempty"`
	// RequireZeroCopy fails unless the selected executor explicitly certifies
	// a device-local video path. Unknown (nil) is not treated as success.
	RequireZeroCopy bool `json:"require_zero_copy,omitempty"`
}

// OverlayRefSpec carries the artifact lineage of the overlay the final video
// composites: the overlay.render queue job id, the frozen OverlayPlan
// fingerprint, the item render key, the source video asset the overlay
// was rendered over, and the declared compositing window (start_us/end_us)
// on the final timeline. It is the join that proves "this final video
// contains THAT overlay" — the final video asset record carries the same
// identity the OverlayPlan / EditingOverlaySpan declared, never a re-derived
// reference.
//
// Omitted (nil) means the final video carries no entity overlay (a plain
// subtitles/watermark clip). When present, every field is required: a
// partial lineage is fail-closed, never a half-proven composition.
type OverlayRefSpec struct {
	// RenderJobID is the overlay.render queue job id that produced the
	// composited overlay artifact.
	RenderJobID string `json:"render_job_id"`
	// PlanFingerprint is the frozen OverlayPlan fingerprint the render was
	// validated against (plan fingerprint == result fingerprint).
	PlanFingerprint string `json:"plan_fingerprint"`
	// RenderKey is the content-addressed render key of the plan item.
	RenderKey string `json:"render_key"`
	// SourceVideoAssetID is the video asset the overlay is composited over
	// (the OverlayPlan's VideoID).
	SourceVideoAssetID string `json:"source_video_asset_id"`
	// StartUS is the declared compositing window start on the final video
	// timeline (integer microseconds, the EditingOverlaySpan.StartUS the
	// request was built from).
	StartUS int64 `json:"start_us"`
	// EndUS is the declared compositing window end on the final video
	// timeline (integer microseconds, the EditingOverlaySpan.EndUS).
	EndUS int64 `json:"end_us"`
}

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
	// Overlay is the optional entity-overlay lineage this final video
	// composites. Nil for subtitles/watermark-only clips.
	Overlay *OverlayRefSpec `json:"overlay,omitempty"`
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
	if r.Watermark.Opacity == 0 {
		r.Watermark.Opacity = 1.0
	}
	if r.Transcript == nil {
		r.Transcript = &TranscriptSpec{}
	}
	if r.Transcript.Mode == "" {
		r.Transcript.Mode = TranscriptModeReuseOrGenerate
	}
	if r.Transcript.Language == "" {
		r.Transcript.Language = DefaultLanguage
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

	switch r.Background.Mode {
	case BackgroundModeNone, BackgroundModeBlurSource:
		// no asset required
	case BackgroundModeAsset:
		if r.Background.AssetID == "" {
			return fmt.Errorf("%w: background.mode=asset requires background.asset_id", ErrInvalidRequest)
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
	case TranscriptModeReuse, TranscriptModeGenerate, TranscriptModeReuseOrGenerate:
	default:
		return fmt.Errorf("%w: transcript.mode must be one of reuse, generate, reuse_or_generate (got %q)", ErrInvalidRequest, r.Transcript.Mode)
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

	switch r.Audio.Mode {
	case AudioModeCopyIfCompatible, AudioModeTranscode:
	default:
		return fmt.Errorf("%w: audio.mode must be one of copy_if_compatible, transcode (got %q)", ErrInvalidRequest, r.Audio.Mode)
	}

	if r.Destination.DriveFolderID == "" {
		return fmt.Errorf("%w: destination.drive_folder_id is required (default DefaultDriveRootFolderID)", ErrInvalidRequest)
	}

	// Overlay lineage is all-or-nothing: a final video that declares an
	// overlay must carry the complete chain (render job id + plan
	// fingerprint + render key + source video asset id + the declared
	// compositing window) so the composition is provable, never a bare "an
	// overlay was here" and never an untimed blend.
	if r.Overlay != nil {
		if strings.TrimSpace(r.Overlay.RenderJobID) == "" ||
			strings.TrimSpace(r.Overlay.PlanFingerprint) == "" ||
			strings.TrimSpace(r.Overlay.RenderKey) == "" ||
			strings.TrimSpace(r.Overlay.SourceVideoAssetID) == "" {
			return fmt.Errorf("%w: overlay requires render_job_id, plan_fingerprint, render_key and source_video_asset_id", ErrInvalidRequest)
		}
		if r.Overlay.StartUS < 0 || r.Overlay.EndUS <= r.Overlay.StartUS {
			return fmt.Errorf("%w: overlay requires a valid compositing window (end_us > start_us >= 0)", ErrInvalidRequest)
		}
	}

	return nil
}

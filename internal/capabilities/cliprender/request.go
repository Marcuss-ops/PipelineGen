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

	// Output media contract.
	OutputContractVeloxEditingClipV1 = "velox-editing-clip-v1"

	// Watermark positions.
	PositionTopLeft     = "top_left"
	PositionTopRight    = "top_right"
	PositionCenter      = "center"
	PositionBottomLeft  = "bottom_left"
	PositionBottomRight = "bottom_right"

	// Output defaults for the Shorts/vertical use case.
	DefaultLanguage = "en"
	DefaultWidth    = 1080
	DefaultHeight   = 1920
	DefaultFPS      = 60

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
	AssetID  string  `json:"asset_id,omitempty"`  // required when enabled
	Position string  `json:"position,omitempty"`  // default top_right
	Opacity  float64 `json:"opacity,omitempty"`   // 0.0–1.0, default 1.0
	MarginPX int     `json:"margin_px,omitempty"` // >= 0, default 0
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
}

// OutputSpec pins the VeloxEditing-compatible output contract. The
// precise codec/pixel/timebase values are owned by the contract
// (follow-up step); the request only selects it + resolution/fps.
type OutputSpec struct {
	Contract string `json:"contract,omitempty"` // default velox-editing-clip-v1
	Width    int    `json:"width,omitempty"`    // default 1080
	Height   int    `json:"height,omitempty"`   // default 1920
	FPS      int    `json:"fps,omitempty"`      // default 60
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
	if r.Output == nil {
		r.Output = &OutputSpec{}
	}
	if r.Output.Contract == "" {
		r.Output.Contract = OutputContractVeloxEditingClipV1
	}
	if r.Output.Width == 0 {
		r.Output.Width = DefaultWidth
	}
	if r.Output.Height == 0 {
		r.Output.Height = DefaultHeight
	}
	if r.Output.FPS == 0 {
		r.Output.FPS = DefaultFPS
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
		if r.Watermark.AssetID == "" {
			return fmt.Errorf("%w: watermark.enabled=true requires watermark.asset_id (a canonical watermark asset, never a raw path)", ErrInvalidRequest)
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
		return fmt.Errorf("%w: output.contract is required (default velox-editing-clip-v1)", ErrInvalidRequest)
	}
	if r.Output.Width < MinDimension || r.Output.Width > MaxDimension {
		return fmt.Errorf("%w: output.width must be within [%d,%d] (got %d)", ErrInvalidRequest, MinDimension, MaxDimension, r.Output.Width)
	}
	if r.Output.Height < MinDimension || r.Output.Height > MaxDimension {
		return fmt.Errorf("%w: output.height must be within [%d,%d] (got %d)", ErrInvalidRequest, MinDimension, MaxDimension, r.Output.Height)
	}
	if r.Output.FPS < MinFPS || r.Output.FPS > MaxFPS {
		return fmt.Errorf("%w: output.fps must be within [%d,%d] (got %d)", ErrInvalidRequest, MinFPS, MaxFPS, r.Output.FPS)
	}

	switch r.Audio.Mode {
	case AudioModeCopyIfCompatible, AudioModeTranscode:
	default:
		return fmt.Errorf("%w: audio.mode must be one of copy_if_compatible, transcode (got %q)", ErrInvalidRequest, r.Audio.Mode)
	}

	if r.Destination.DriveFolderID == "" {
		return fmt.Errorf("%w: destination.drive_folder_id is required (default DefaultDriveRootFolderID)", ErrInvalidRequest)
	}

	return nil
}

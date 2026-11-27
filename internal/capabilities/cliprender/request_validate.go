package cliprender

// request_validate.go owns the fail-fast request gate. Validate is an
// orchestrator: one validator per nested block, evaluated in the historical
// order so the first failing dimension and its message stay identical.

import (
	"context"
	"fmt"
	"strings"
)

// Validate performs the fail-fast contract gate. Call Normalize first —
// zero values are interpreted per the canonical defaults, so a request
// that was never normalized is validated against those same defaults.
func (r *RenderRequest) Validate() error {
	if err := validateRequestEnvelope(r); err != nil {
		return err
	}
	if err := validateBackgroundSpec(r.Background); err != nil {
		return err
	}
	if err := validateWatermarkSpec(r.Watermark); err != nil {
		return err
	}
	if err := validateTranscriptSpec(r.Transcript); err != nil {
		return err
	}
	if err := validateSubtitlesSpec(r.Subtitles); err != nil {
		return err
	}
	if err := validateOutputSpec(r.Output); err != nil {
		return err
	}
	if err := validateAudioSpec(r.Audio); err != nil {
		return err
	}
	if err := validateDestinationSpec(r.Destination); err != nil {
		return err
	}
	if err := validateOverlayLineages(r.Overlays); err != nil {
		return err
	}

	// Resolve the declared assembly contract at the request boundary. The
	// registry is closed, and the frozen V1 lane accepts exactly 24/1: without
	// this check an unsupported contract or a 25 fps V1 request would be
	// accepted (202), rendered on the GPU, and only then rejected by the
	// canonical emission gate at the end of the pipeline.
	if _, err := NewContractResolver().Resolve(context.Background(), r); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return nil
}

func validateRequestEnvelope(r *RenderRequest) error {
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
	for _, pin := range []struct {
		label    string
		revision string
	}{
		{"source_revision", r.SourceRevision},
		{"background.revision", revisionOrEmpty(r.Background)},
		{"watermark.revision", revisionOrEmptyWatermark(r.Watermark)},
		{"transcript.revision", revisionOrEmptyTranscript(r.Transcript)},
	} {
		if pin.revision == "" {
			continue
		}
		if normalizeSHA256(pin.revision) == "" {
			return fmt.Errorf("%w: %s must be a SHA-256 hex digest when present (got %q)", ErrInvalidRequest, pin.label, pin.revision)
		}
	}

	if r.Execution != nil && r.Execution.RequireZeroCopy {
		return fmt.Errorf("%w: execution.require_zero_copy=true is unsatisfiable: no backend certifies video_zero_copy (GPU compositing is Chronon-only via the RenderingGen queue)", ErrInvalidRequest)
	}
	return nil
}

func revisionOrEmpty(b *BackgroundSpec) string {
	if b == nil {
		return ""
	}
	return b.Revision
}

func revisionOrEmptyWatermark(w *WatermarkSpec) string {
	if w == nil {
		return ""
	}
	return w.Revision
}

func revisionOrEmptyTranscript(t *TranscriptSpec) string {
	if t == nil {
		return ""
	}
	return t.Revision
}

func validateBackgroundSpec(background *BackgroundSpec) error {
	switch background.Mode {
	case BackgroundModeNone, BackgroundModeBlurSource:
		// No asset and therefore no media family: a declared kind would be an
		// unresolved contradiction (which plate? the mode says there is none).
		if background.Kind != "" {
			return fmt.Errorf("%w: background.kind must be empty for mode=%s (the mode carries no asset)", ErrInvalidRequest, background.Mode)
		}
	case BackgroundModeAsset:
		if background.AssetID == "" {
			return fmt.Errorf("%w: background.mode=asset requires background.asset_id", ErrInvalidRequest)
		}
		if background.Kind != "" && !IsBackgroundKind(background.Kind) {
			return fmt.Errorf("%w: background.kind must be one of %s, %s (got %q)", ErrInvalidRequest, BackgroundKindImage, BackgroundKindVideo, background.Kind)
		}
	default:
		return fmt.Errorf("%w: background.mode must be one of none, blur_source, asset (got %q)", ErrInvalidRequest, background.Mode)
	}
	if err := ValidateBackgroundTreatment(background.Mode, background.BlurPX, background.Zoom); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return nil
}

func validateWatermarkSpec(watermark *WatermarkSpec) error {
	if !watermark.Enabled {
		return nil
	}
	if strings.TrimSpace(watermark.AssetID) == "" && strings.TrimSpace(watermark.Text) == "" {
		return fmt.Errorf("%w: watermark.enabled=true requires watermark.asset_id or watermark.text", ErrInvalidRequest)
	}
	switch watermark.Position {
	case PositionTopLeft, PositionTopRight, PositionCenter, PositionBottomLeft, PositionBottomRight:
	default:
		return fmt.Errorf("%w: watermark.position must be one of top_left, top_right, center, bottom_left, bottom_right (got %q)", ErrInvalidRequest, watermark.Position)
	}
	if watermark.Opacity < 0 || watermark.Opacity > 1 {
		return fmt.Errorf("%w: watermark.opacity must be within [0,1] (got %v)", ErrInvalidRequest, watermark.Opacity)
	}
	if watermark.MarginPX < 0 {
		return fmt.Errorf("%w: watermark.margin_px must be >= 0 (got %d)", ErrInvalidRequest, watermark.MarginPX)
	}
	return nil
}

func validateTranscriptSpec(transcript *TranscriptSpec) error {
	switch transcript.Mode {
	case TranscriptModeAuto, TranscriptModeReuse, TranscriptModeGenerate:
	default:
		return fmt.Errorf("%w: transcript.mode must be one of auto, reuse, generate (got %q)", ErrInvalidRequest, transcript.Mode)
	}
	if transcript.Language == "" {
		return fmt.Errorf("%w: transcript.language is required (default en)", ErrInvalidRequest)
	}
	return nil
}

func validateSubtitlesSpec(subtitles *SubtitlesSpec) error {
	if !subtitles.Enabled {
		return nil
	}
	switch subtitles.Mode {
	case SubtitlesModeBurn, SubtitlesModeSidecar:
	default:
		return fmt.Errorf("%w: subtitles.mode must be one of burn, sidecar (got %q)", ErrInvalidRequest, subtitles.Mode)
	}
	return nil
}

func validateOutputSpec(output *OutputSpec) error {
	if output.Contract == "" {
		return fmt.Errorf("%w: output.contract is required (default VELOX_ASSEMBLY_READY_V1)", ErrInvalidRequest)
	}
	if output.Width < MinDimension || output.Width > MaxDimension {
		return fmt.Errorf("%w: output.width must be within [%d,%d] (got %d)", ErrInvalidRequest, MinDimension, MaxDimension, output.Width)
	}
	if output.Height < MinDimension || output.Height > MaxDimension {
		return fmt.Errorf("%w: output.height must be within [%d,%d] (got %d)", ErrInvalidRequest, MinDimension, MaxDimension, output.Height)
	}
	if output.Width <= output.Height {
		return fmt.Errorf("%w: vertical output is disabled; YouTube clips must be horizontal (width %d must be greater than height %d)", ErrInvalidRequest, output.Width, output.Height)
	}
	if output.FPSNum <= 0 || output.FPSDen <= 0 {
		return fmt.Errorf("%w: output.fps_num/fps_den must be positive (got %d/%d)", ErrInvalidRequest, output.FPSNum, output.FPSDen)
	}
	fps := float64(output.FPSNum) / float64(output.FPSDen)
	if fps < MinFPS || fps > MaxFPS {
		return fmt.Errorf("%w: output.fps must be within [%d,%d] (got %d/%d = %.3f)", ErrInvalidRequest, MinFPS, MaxFPS, output.FPSNum, output.FPSDen, fps)
	}
	if output.ForegroundScalePercent < 0 || output.ForegroundScalePercent > 100 {
		return fmt.Errorf("%w: output.foreground_scale_percent must be within [0,100], where 0 selects the contract default (100) (got %d)", ErrInvalidRequest, output.ForegroundScalePercent)
	}
	// One validator for the card block, shared with the sealed plan, so the
	// request and the plan cannot accept different source_frame shapes.
	if err := ValidatePlanSourceFrame(output.SourceFrame, output.ForegroundScalePercent); err != nil {
		return fmt.Errorf("%w: output.%w", ErrInvalidRequest, err)
	}
	if err := ValidateClipEffects(output.Effects, output.Grid); err != nil {
		return fmt.Errorf("%w: output.%w", ErrInvalidRequest, err)
	}
	return nil
}

func validateAudioSpec(audio *AudioSpec) error {
	switch audio.Mode {
	case AudioModeCopyIfCompatible:
		return nil
	case AudioModeTranscode:
		return fmt.Errorf("%w: audio.mode=transcode is not supported: the native mux copies the source stream; use copy_if_compatible", ErrInvalidRequest)
	default:
		return fmt.Errorf("%w: audio.mode must be copy_if_compatible (got %q)", ErrInvalidRequest, audio.Mode)
	}
}

func validateDestinationSpec(destination *DestinationSpec) error {
	if destination.DriveFolderID == "" {
		return fmt.Errorf("%w: destination.drive_folder_id is required (default DefaultDriveRootFolderID)", ErrInvalidRequest)
	}
	return nil
}

// Overlay lineages are all-or-nothing, EACH of them: a final video that
// declares overlays must carry the complete chain (render job id + plan
// fingerprint + render key + source video asset id + the declared compositing
// window) for every one, so the composition is provable, never a bare "an
// overlay was here" and never an untimed blend. A partial lineage is rejected
// rather than dropped: silently compositing three of four declared overlays is
// the failure this list contract exists to stop.
func validateOverlayLineages(overlays []OverlayRefSpec) error {
	for i, overlay := range overlays {
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

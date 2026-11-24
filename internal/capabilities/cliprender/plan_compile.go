package cliprender

// plan_compile.go owns Compile, the fail-closed builder of ClipRenderPlanV1.
// Every value comes from the fully-resolved preparation phase; Compile never
// resolves anything itself. The body is split by plan block so each block has
// one owner and one error contract.

import (
	"fmt"
	"strings"
)

// Compile builds the sealed plan from the resolved inputs. Fail-closed: any
// missing input (nil source, nil contract, empty output path) is a typed
// error — the plan is never partially resolved.
func Compile(in CompileInput) (ClipRenderPlanV1, error) {
	if err := validateCompileInput(in); err != nil {
		return ClipRenderPlanV1{}, err
	}
	plan := newPlanSkeleton(in)
	if err := applyPlanBackground(&plan, in); err != nil {
		return ClipRenderPlanV1{}, err
	}
	if err := applyPlanWatermark(&plan, in); err != nil {
		return ClipRenderPlanV1{}, err
	}
	if err := applyPlanSubtitles(&plan, in); err != nil {
		return ClipRenderPlanV1{}, err
	}
	if err := applyPlanOverlay(&plan, in); err != nil {
		return ClipRenderPlanV1{}, err
	}
	if err := ValidateClipEffects(plan.Output.Effects, plan.Output.Grid); err != nil {
		return ClipRenderPlanV1{}, fmt.Errorf("%w: output.%w", ErrInvalidClipPlan, err)
	}
	if err := plan.Seal(); err != nil {
		return ClipRenderPlanV1{}, err
	}
	if err := plan.Validate(); err != nil {
		return ClipRenderPlanV1{}, err
	}
	return plan, nil
}

func validateCompileInput(in CompileInput) error {
	if in.RunID == "" {
		return fmt.Errorf("%w: run_id is required", ErrInvalidClipPlan)
	}
	if in.Source == nil || in.Source.LocalPath == "" || !isSHA256Hex(in.Source.SHA256) {
		return fmt.Errorf("%w: source path + sha256 are required", ErrInvalidClipPlan)
	}
	if in.Contract == nil {
		return fmt.Errorf("%w: output contract is required", ErrInvalidClipPlan)
	}
	if strings.TrimSpace(in.OutputPath) == "" {
		return fmt.Errorf("%w: output_path is required", ErrInvalidClipPlan)
	}
	switch in.AudioMode {
	case AudioModeCopyIfCompatible:
	case AudioModeTranscode:
		return fmt.Errorf("%w: audio mode transcode is not supported: the native mux copies the source stream", ErrInvalidClipPlan)
	default:
		return fmt.Errorf("%w: audio mode must be copy_if_compatible (got %q)", ErrInvalidClipPlan, in.AudioMode)
	}
	return nil
}

func newPlanSkeleton(in CompileInput) ClipRenderPlanV1 {
	return ClipRenderPlanV1{
		Version:    PlanVersion,
		RunID:      in.RunID,
		DurationMS: in.DurationMS,
		Source: PlanSource{
			AssetID: in.Source.AssetID,
			Path:    in.Source.LocalPath,
			SHA256:  in.Source.SHA256,
		},
		Output: PlanOutput{
			ContractID:             in.Contract.ID,
			Container:              in.Contract.Container,
			VideoCodec:             in.Contract.VideoCodec,
			VideoProfile:           in.Contract.VideoProfile,
			PixelFormat:            in.Contract.PixelFormat,
			Width:                  in.Contract.Width,
			Height:                 in.Contract.Height,
			FPSNum:                 in.Contract.FPS.Num,
			FPSDen:                 in.Contract.FPS.Den,
			ForegroundScalePercent: normalizeForegroundScale(in.ForegroundScalePercent),
			SourceFrame:            in.SourceFrame,
			Effects:                append([]ClipEffectSpec(nil), in.Effects...),
			Grid:                   in.Grid,
		},
		Audio: PlanAudio{
			Mode:       in.AudioMode,
			Codec:      in.Contract.AudioCodec,
			SampleRate: in.Contract.AudioSampleRate,
			Channels:   in.Contract.AudioChannels,
		},
		OutputPath: strings.TrimSpace(in.OutputPath),
	}
}

// Background mode is a request-level business selection resolved here;
// blur_source carries no asset block (the renderer derives the blurred
// background from the source itself), mode=asset requires the verified
// materialized asset, and none requires no asset.
func applyPlanBackground(plan *ClipRenderPlanV1, in CompileInput) error {
	backgroundMode := in.BackgroundMode
	if backgroundMode == "" {
		if in.Background == nil {
			backgroundMode = BackgroundModeNone
		} else {
			backgroundMode = BackgroundModeAsset
		}
	}
	// A media family is only meaningful for a mode that carries a plate: a
	// kind on an asset-less mode is a contradiction (the mode says there is no
	// background) and is refused rather than dropped, so the caller learns the
	// two inputs disagreed.
	if in.BackgroundKind != "" && backgroundMode != BackgroundModeAsset {
		return fmt.Errorf("%w: background_kind must be empty for mode=%s (got %q)", ErrInvalidClipPlan, backgroundMode, in.BackgroundKind)
	}
	if err := ValidateBackgroundTreatment(backgroundMode, in.BackgroundBlurPX, in.BackgroundZoom); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidClipPlan, err)
	}
	switch backgroundMode {
	case BackgroundModeNone:
		if in.Background != nil {
			return fmt.Errorf("%w: background mode=none must not carry an asset", ErrInvalidClipPlan)
		}
		plan.Background = &PlanBackground{Mode: BackgroundModeNone}
	case BackgroundModeBlurSource:
		if in.Background != nil {
			return fmt.Errorf("%w: background mode=blur_source must not carry an asset", ErrInvalidClipPlan)
		}
		plan.Background = &PlanBackground{
			Mode:   BackgroundModeBlurSource,
			BlurPX: in.BackgroundBlurPX,
			Zoom:   in.BackgroundZoom,
		}
	case BackgroundModeAsset:
		if in.Background == nil || in.Background.LocalPath == "" || !isSHA256Hex(in.Background.SHA256) {
			return fmt.Errorf("%w: background mode=asset requires the materialized asset with path + sha256", ErrInvalidClipPlan)
		}
		// The media family is REQUIRED and validated here: the renderer samples
		// an image plate and a video plate through two different layers, so a
		// plan that cannot name the family is not renderable. It is never
		// guessed from the file extension — the preparer resolves it from the
		// asset's canonical MediaType before the plan is sealed.
		if !IsBackgroundKind(in.BackgroundKind) {
			return fmt.Errorf("%w: background mode=asset requires background_kind to be one of %s, %s (got %q)", ErrInvalidClipPlan, BackgroundKindImage, BackgroundKindVideo, in.BackgroundKind)
		}
		plan.Background = &PlanBackground{
			Mode:    BackgroundModeAsset,
			Kind:    in.BackgroundKind,
			AssetID: in.Background.AssetID,
			Path:    in.Background.LocalPath,
			SHA256:  in.Background.SHA256,
		}
	default:
		return fmt.Errorf("%w: invalid background mode %q", ErrInvalidClipPlan, backgroundMode)
	}
	return nil
}

func applyPlanWatermark(plan *ClipRenderPlanV1, in CompileInput) error {
	if in.Watermark == nil && (in.WatermarkSpec == nil || strings.TrimSpace(in.WatermarkSpec.Text) == "") {
		return nil
	}
	if in.Watermark != nil && in.Watermark.LocalPath == "" {
		return fmt.Errorf("%w: watermark asset requires path + sha256", ErrInvalidClipPlan)
	}
	// WatermarkSpec is the single source for text/position/opacity/margin
	// and style; there is no second text spelling.
	wm := in.WatermarkSpec
	if wm == nil {
		wm = &WatermarkSpec{Position: PositionTopRight, Opacity: 1.0}
	}
	text := strings.TrimSpace(wm.Text)
	if text == "" && (in.Watermark == nil || !isSHA256Hex(in.Watermark.SHA256)) {
		return fmt.Errorf("%w: watermark asset requires path + sha256", ErrInvalidClipPlan)
	}
	assetID, path, sha := "", "", ""
	if in.Watermark != nil {
		assetID, path, sha = in.Watermark.AssetID, in.Watermark.LocalPath, in.Watermark.SHA256
	}
	plan.Watermark = &PlanWatermark{
		Text:     text,
		AssetID:  assetID,
		Path:     path,
		SHA256:   sha,
		Position: wm.Position,
		Opacity:  wm.Opacity,
		MarginPX: wm.MarginPX,
		Style:    cloneVisualStyle(wm.Style),
	}
	return nil
}

func applyPlanSubtitles(plan *ClipRenderPlanV1, in CompileInput) error {
	if in.Subtitles == nil {
		return nil
	}
	if in.Subtitles.LocalPath == "" || !isSHA256Hex(in.Subtitles.SHA256) {
		return fmt.Errorf("%w: subtitle ASS requires path + sha256", ErrInvalidClipPlan)
	}
	plan.Subtitles = &PlanSubtitles{
		Mode:    in.Subtitles.Mode,
		StyleID: in.Subtitles.StyleID,
		Path:    in.Subtitles.LocalPath,
		SHA256:  in.Subtitles.SHA256,
		Cues:    append([]Cue(nil), in.Cues...),
		Style:   cloneVisualStyle(in.SubtitlesStyle),
	}
	return nil
}

func applyPlanOverlay(plan *ClipRenderPlanV1, in CompileInput) error {
	if in.Overlay == nil {
		return nil
	}
	if len(in.Overlay.Segments) == 0 {
		return fmt.Errorf("%w: overlay requires at least one resolved segment", ErrInvalidClipPlan)
	}
	segments := make([]PlanOverlaySegment, 0, len(in.Overlay.Segments))
	for i, input := range in.Overlay.Segments {
		seg := input.Segment
		if seg == nil || seg.LocalPath == "" || !isSHA256Hex(seg.SHA256) {
			return fmt.Errorf("%w: overlay segment %d requires the materialized segment with path + sha256", ErrInvalidClipPlan, i)
		}
		if input.StartMS < 0 || input.EndMS <= input.StartMS {
			return fmt.Errorf("%w: overlay segment %d window [%d, %d) is invalid", ErrInvalidClipPlan, i, input.StartMS, input.EndMS)
		}
		if in.DurationMS > 0 && input.EndMS > in.DurationMS {
			return fmt.Errorf("%w: overlay segment %d window end %dms exceeds the clip duration %dms", ErrInvalidClipPlan, i, input.EndMS, in.DurationMS)
		}
		segments = append(segments, PlanOverlaySegment{
			RenderJobID: seg.RenderJobID,
			RenderKey:   seg.RenderKey,
			Path:        seg.LocalPath,
			SHA256:      seg.SHA256,
			SizeBytes:   seg.SizeBytes,
			StartMS:     input.StartMS,
			EndMS:       input.EndMS,
		})
	}
	plan.Overlay = &PlanOverlay{Segments: segments}
	return nil
}

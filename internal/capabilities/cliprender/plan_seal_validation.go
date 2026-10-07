package cliprender

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func normalizeForegroundScale(value int) int {
	if value == 0 {
		return 100
	}
	return value
}

// cloneVisualStyle copies the canonical visual style block so the sealed
// plan never shares a pointer with the caller's mutable request. A nil input
// stays nil (no empty block on the wire).
func cloneVisualStyle(in *scriptpkg.VideoVisualStyleSpec) *scriptpkg.VideoVisualStyleSpec {
	if in == nil {
		return nil
	}
	out := *in
	if in.Stroke != nil {
		stroke := *in.Stroke
		out.Stroke = &stroke
	}
	if in.Shadow != nil {
		shadow := *in.Shadow
		out.Shadow = &shadow
	}
	if in.TransitionIn != nil {
		transition := *in.TransitionIn
		out.TransitionIn = &transition
	}
	return &out
}

// Seal computes the deterministic PlanSHA256 over the plan content.
func (p *ClipRenderPlanV1) Seal() error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrInvalidClipPlan)
	}
	hash, err := p.Hash()
	if err != nil {
		return err
	}
	p.PlanSHA256 = hash
	return nil
}

// Hash computes the deterministic digest with PlanSHA256 zeroed (so sealing
// is idempotent and drift is detectable).
func (p ClipRenderPlanV1) Hash() (string, error) {
	copyPlan := p
	copyPlan.PlanSHA256 = ""
	b, err := json.Marshal(copyPlan)
	if err != nil {
		return "", fmt.Errorf("hash clip render plan: %w", err)
	}
	sum := digest.SHA256Bytes(b)
	return sum, nil
}

// Validate enforces the plan contract fail-closed: identity, resolved blocks,
// matching hashes, valid enum values, and a PlanSHA256 that matches the
// content (tamper / partial-mutation detection).
func (p ClipRenderPlanV1) Validate() error {
	if p.Version != PlanVersion || p.RunID == "" || p.OutputPath == "" {
		return fmt.Errorf("%w: version, run_id, or output_path missing", ErrInvalidClipPlan)
	}
	if p.Source.AssetID == "" || p.Source.Path == "" || !digest.IsCanonicalSHA256(p.Source.SHA256) {
		return fmt.Errorf("%w: source must carry asset_id, path, and sha256", ErrInvalidClipPlan)
	}
	if p.Output.ContractID == "" || p.Output.Container == "" || p.Output.VideoCodec == "" ||
		p.Output.PixelFormat == "" || p.Output.Width <= 0 || p.Output.Height <= 0 ||
		p.Output.FPSNum <= 0 || p.Output.FPSDen <= 0 {
		return fmt.Errorf("%w: output contract is incomplete", ErrInvalidClipPlan)
	}
	if p.Output.ForegroundScalePercent != 0 && (p.Output.ForegroundScalePercent < 1 || p.Output.ForegroundScalePercent > 100) {
		return fmt.Errorf("%w: foreground_scale_percent must be within [1,100]", ErrInvalidClipPlan)
	}
	if err := ValidatePlanSourceFrame(p.Output.SourceFrame, p.Output.ForegroundScalePercent); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidClipPlan, err)
	}
	switch p.Audio.Mode {
	case AudioModeCopyIfCompatible, AudioModeTranscode:
	default:
		return fmt.Errorf("%w: invalid audio mode %q", ErrInvalidClipPlan, p.Audio.Mode)
	}
	if p.Audio.Codec == "" || p.Audio.SampleRate <= 0 || p.Audio.Channels <= 0 {
		return fmt.Errorf("%w: audio contract is incomplete", ErrInvalidClipPlan)
	}

	if p.Background != nil {
		switch p.Background.Mode {
		case BackgroundModeNone, BackgroundModeBlurSource:
			// No asset (and therefore no media family) is required — and a
			// declared kind is a contradiction, not extra information.
			if p.Background.Kind != "" {
				return fmt.Errorf("%w: background mode=%s must not carry a kind (got %q)", ErrInvalidClipPlan, p.Background.Mode, p.Background.Kind)
			}
		case BackgroundModeAsset:
			if p.Background.AssetID == "" || p.Background.Path == "" || !digest.IsCanonicalSHA256(p.Background.SHA256) {
				return fmt.Errorf("%w: background mode=asset requires asset_id, path, and sha256", ErrInvalidClipPlan)
			}
			if !IsBackgroundKind(p.Background.Kind) {
				return fmt.Errorf("%w: background mode=asset requires kind to be one of %s, %s (got %q)", ErrInvalidClipPlan, BackgroundKindImage, BackgroundKindVideo, p.Background.Kind)
			}
		default:
			return fmt.Errorf("%w: invalid background mode %q", ErrInvalidClipPlan, p.Background.Mode)
		}
	}

	if p.Watermark != nil {
		if strings.TrimSpace(p.Watermark.Text) == "" && (p.Watermark.AssetID == "" || p.Watermark.Path == "" || !digest.IsCanonicalSHA256(p.Watermark.SHA256)) {
			return fmt.Errorf("%w: watermark requires asset_id, path, and sha256", ErrInvalidClipPlan)
		}
		switch p.Watermark.Position {
		case PositionTopLeft, PositionTopRight, PositionCenter, PositionBottomLeft, PositionBottomRight:
		default:
			return fmt.Errorf("%w: invalid watermark position %q", ErrInvalidClipPlan, p.Watermark.Position)
		}
		if p.Watermark.Opacity < 0 || p.Watermark.Opacity > 1 {
			return fmt.Errorf("%w: watermark opacity must be within [0,1]", ErrInvalidClipPlan)
		}
		if p.Watermark.MarginPX < 0 {
			return fmt.Errorf("%w: watermark margin_px must be >= 0", ErrInvalidClipPlan)
		}
	}

	if p.Subtitles != nil {
		switch p.Subtitles.Mode {
		case SubtitlesModeBurn, SubtitlesModeSidecar:
		default:
			return fmt.Errorf("%w: invalid subtitle mode %q", ErrInvalidClipPlan, p.Subtitles.Mode)
		}
		if p.Subtitles.Path == "" || !digest.IsCanonicalSHA256(p.Subtitles.SHA256) {
			return fmt.Errorf("%w: subtitles require an ASS path + sha256", ErrInvalidClipPlan)
		}
	}

	if p.Overlay != nil {
		// A declared overlay with no segment is the single worst outcome: the
		// plan claims an overlay and composites nothing. Fail closed.
		if len(p.Overlay.Segments) == 0 {
			return fmt.Errorf("%w: overlay declared with no segments", ErrInvalidClipPlan)
		}
		for i, seg := range p.Overlay.Segments {
			if seg.Path == "" || !digest.IsCanonicalSHA256(seg.SHA256) {
				return fmt.Errorf("%w: overlay segment %d requires a segment path + sha256", ErrInvalidClipPlan, i)
			}
			if seg.StartMS < 0 || seg.EndMS <= seg.StartMS {
				return fmt.Errorf("%w: overlay segment %d window [%d, %d) is invalid", ErrInvalidClipPlan, i, seg.StartMS, seg.EndMS)
			}
			if p.DurationMS > 0 && seg.EndMS > p.DurationMS {
				return fmt.Errorf("%w: overlay segment %d window end %dms exceeds the clip duration %dms", ErrInvalidClipPlan, i, seg.EndMS, p.DurationMS)
			}
		}
	}

	expected, err := p.Hash()
	if err != nil {
		return err
	}
	if p.PlanSHA256 != expected {
		return fmt.Errorf("%w: got %q want %q", ErrClipPlanDrift, p.PlanSHA256, expected)
	}
	return nil
}

// isSHA256Hex preserves the package-level validation seam used by plan,
// continuation and chunk contracts while delegating to the digest SSOT.
func isSHA256Hex(value string) bool { return digest.IsCanonicalSHA256(value) }

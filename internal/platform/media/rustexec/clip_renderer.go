package rustexec

// clip_renderer.go — the Go adapter for the render_clip Rust operation
// (feature spec §6). Mirrors StockRenderer: the sealed ClipRenderPlanV1 is
// re-validated and every referenced artifact is verified BEFORE any Rust
// process starts; the encoder policy comes from the composition-root media
// config; Rust executes the plan verbatim in a single render pass and
// reports the copy-policy + CPU-subtitle outcomes natively.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

// ClipRenderResult is the typed outcome of a render_clip execution. Every
// value comes from the Rust response metadata (single ownership of media
// facts) plus a fail-closed local existence/size check on the output.
type ClipRenderResult struct {
	OutputPath        string
	SizeBytes         int64
	DurationSec       float64
	Width             uint32
	Height            uint32
	FPS               uint32
	FFmpegMS          int64
	AudioCopyEligible *bool
	AudioEncodePasses *int
	SubtitleRasterCPU *bool
}

// ClipRenderer executes sealed ClipRenderPlanV1 plans through the Rust
// render_clip operation.
type ClipRenderer struct {
	client  *Client
	policy  mediaexec.EncoderPolicy
	profile mediaexec.VideoProfile
}

func NewClipRenderer(binaryPath, ffmpegPath string, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *ClipRenderer {
	return NewClipRendererWithExecutor(NewExecutor(binaryPath, ffmpegPath, log), policy, profile, log)
}

func NewClipRendererWithExecutor(executor *Executor, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *ClipRenderer {
	return &ClipRenderer{client: NewClientWithExecutor(executor, log), policy: policy, profile: profile}
}

// RenderClip executes the sealed plan. Fail-closed sequence:
//  1. plan.Validate() — identity + hashes + enum gates + PlanSHA256 drift;
//  2. every referenced artifact (source, background asset, watermark,
//     subtitles) must exist locally — the plan is the last audited contract;
//  3. the encoder policy + profile come from the composition-root media
//     config (Rust never chooses codec/quality/GOP);
//  4. geometry is transported from the plan so the Rust side can detect any
//     profile/plan drift;
//  5. the output must exist and be non-empty after a successful response.
func (r *ClipRenderer) RenderClip(ctx context.Context, plan cliprender.ClipRenderPlanV1) (ClipRenderResult, error) {
	if err := plan.Validate(); err != nil {
		return ClipRenderResult{}, fmt.Errorf("clip render plan validation failed: %w", err)
	}
	if err := verifyClipPlanArtifacts(plan); err != nil {
		return ClipRenderResult{}, err
	}
	codec, preset, crf, err := (&VideoProcessor{client: r.client, policy: r.policy, profile: r.profile}).policyFor("", "", 0)
	if err != nil {
		return ClipRenderResult{}, err
	}
	profile := r.profile
	if profile == (mediaexec.VideoProfile{}) {
		profile = mediaexec.VideoProfile{}.WithDefaults()
	}
	if err := validateResolvedProfile(profile); err != nil {
		return ClipRenderResult{}, err
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return ClipRenderResult{}, fmt.Errorf("marshal clip render plan: %w", err)
	}
	result, err := r.client.call(ctx, request{
		Operation:        OperationRenderClip,
		SourcePath:       plan.Source.Path,
		OutputPath:       plan.OutputPath,
		Codec:            codec,
		Preset:           preset,
		CRF:              crf,
		Width:            uint32(plan.Output.Width),
		Height:           uint32(plan.Output.Height),
		FPS:              uint32(plan.Output.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec:       plan.Audio.Codec,
		AudioBitrate:     profile.AudioBitrate,
		SampleRate:       uint32(plan.Audio.SampleRate),
		Channels:         uint32(plan.Audio.Channels),
		ClipPlan:         planJSON,
	})
	if err != nil {
		return ClipRenderResult{}, fmt.Errorf("execute clip render: %w", err)
	}
	if result.Metadata == nil {
		return ClipRenderResult{}, fmt.Errorf("render_clip returned no metadata")
	}
	info, err := os.Stat(plan.OutputPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		if err == nil {
			err = fmt.Errorf("output is empty")
		}
		return ClipRenderResult{}, fmt.Errorf("render_clip output missing or empty: %w", err)
	}
	m := result.Metadata
	return ClipRenderResult{
		OutputPath:        plan.OutputPath,
		SizeBytes:         info.Size(),
		DurationSec:       m.DurationSec,
		Width:             m.Width,
		Height:            m.Height,
		FPS:               uint32(m.FPS),
		FFmpegMS:          m.FFmpegMS,
		AudioCopyEligible: m.AudioCopyEligible,
		AudioEncodePasses: m.AudioEncodePasses,
		SubtitleRasterCPU: m.SubtitleRasterCPU,
	}, nil
}

// verifyClipPlanArtifacts fail-closes on any referenced artifact missing
// from disk. Content-hash verification is owned by Rust (render_clip
// re-audits every file with sha256sum); this existence check closes the
// replacement window between plan validation and process execution.
func verifyClipPlanArtifacts(plan cliprender.ClipRenderPlanV1) error {
	required := []struct {
		label string
		path  string
	}{
		{label: "source", path: plan.Source.Path},
	}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		required = append(required, struct {
			label string
			path  string
		}{label: "background", path: plan.Background.Path})
	}
	if plan.Watermark != nil {
		required = append(required, struct {
			label string
			path  string
		}{label: "watermark", path: plan.Watermark.Path})
	}
	if plan.Subtitles != nil {
		required = append(required, struct {
			label string
			path  string
		}{label: "subtitles", path: plan.Subtitles.Path})
	}
	for _, artifact := range required {
		info, err := os.Stat(artifact.path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			if err == nil {
				err = fmt.Errorf("not a regular file or empty")
			}
			return fmt.Errorf("clip render plan artifact %s %q unavailable: %w", artifact.label, artifact.path, err)
		}
	}
	return nil
}

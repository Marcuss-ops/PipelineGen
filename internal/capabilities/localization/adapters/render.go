package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// RenderPlanExecutor implements localization.RenderPlanExecutor.
// It is fail-closed: an unwired render executor, an invalid plan, or a missing
// source/subtitle artifact is a typed error before the Chronon render starts.
type RenderPlanExecutor struct {
	renderer cliprender.RenderExecutor
	profile  mediaexec.VideoProfile
	log      *zap.Logger
}

// NewRenderPlanExecutor builds the bridge. profile is normalized to defaults
// so a partially-populated composition-root profile is safe. log is required
// so every phase of the execute pipeline is observable.
func NewRenderPlanExecutor(renderer cliprender.RenderExecutor, profile mediaexec.VideoProfile, log *zap.Logger) *RenderPlanExecutor {
	if log == nil {
		log = zap.NewNop()
	}
	return &RenderPlanExecutor{renderer: renderer, profile: profile.WithDefaults(), log: log}
}

var _ localization.RenderPlanExecutor = (*RenderPlanExecutor)(nil)

func (a *RenderPlanExecutor) logPhase(phase, planID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "localization_render"),
		zap.String("phase", phase),
		zap.String("plan_revision", planID),
	}, fields...)
	a.log.Info("clip.render.localization.phase", all...)
}

// Execute maps the sealed render.RenderPlan + subtitle ASS into a concrete
// ClipRenderPlanV1 and runs it through the render_clip boundary. The returned
// RenderFacts carry the certified output path, the content SHA-256 (read from
// the actual bytes on disk), the size, the duration, and the codecs pinned by
// the output contract (Chronon re-audits these before reporting success).
func (a *RenderPlanExecutor) Execute(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset) (localization.RenderFacts, error) {
	return a.execute(ctx, plan, subtitle, localization.RenderOptions{})
}

// ExecuteWithWatermark keeps watermarking on the same sealed render_clip
// invocation as subtitle burn. There is no second device/encode pass.
// Deprecated in favor of ExecuteExtended: this watermark-only variant cannot
// carry background or subtitle style to the sealed plan.
func (a *RenderPlanExecutor) ExecuteWithWatermark(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset, watermark *cliprender.MaterializedAsset, spec *cliprender.WatermarkSpec) (localization.RenderFacts, error) {
	return a.execute(ctx, plan, subtitle, localization.RenderOptions{
		Watermark:     watermark,
		WatermarkSpec: spec,
	})
}

// ExecuteExtended runs the full-fidelity render_clip invocation: watermark,
// background, and subtitle style all reach the sealed ClipRenderPlanV1 on the
// same single render pass (no second device/encode pass).
func (a *RenderPlanExecutor) ExecuteExtended(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset, opts localization.RenderOptions) (localization.RenderFacts, error) {
	return a.execute(ctx, plan, subtitle, opts)
}

func (a *RenderPlanExecutor) execute(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset, opts localization.RenderOptions) (localization.RenderFacts, error) {
	if a == nil || a.renderer == nil {
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan executor not wired")
	}
	if err := plan.Validate(); err != nil {
		a.logPhase("validate_failed", plan.Revision, zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan validation failed: %w", err)
	}
	if len(plan.Manifest) == 0 {
		a.logPhase("validate_failed", plan.Revision, zap.String("reason", "empty_manifest"))
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan has no source manifest entry")
	}
	src := plan.Manifest[0]
	if src.Path == "" || src.SHA256 == "" {
		a.logPhase("validate_failed", plan.Revision, zap.String("reason", "incomplete_source"))
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan source is incomplete")
	}

	var sub *cliprender.SubtitleArtifact
	if subtitle != nil {
		if subtitle.LocalPath == "" || subtitle.SHA256 == "" {
			a.logPhase("validate_failed", plan.Revision, zap.String("reason", "incomplete_subtitle"))
			return localization.RenderFacts{}, fmt.Errorf("localization: subtitle ASS is incomplete")
		}
		sub = &cliprender.SubtitleArtifact{
			LocalPath: subtitle.LocalPath,
			SHA256:    subtitle.SHA256,
			Mode:      cliprender.SubtitlesModeBurn,
			StyleID:   subtitle.StyleHash,
		}
	}

	// The output contract is pinned by the composition-root profile + the
	// plan's nominal frame rate (the render plan carries no geometry/pixel
	// facts — those are the profile's single canonical owner).
	contract := &cliprender.ResolvedContract{
		ContractID:   cliprender.OutputContractVeloxAssemblyReadyV1,
		Container:    "mp4",
		VideoCodec:   "h264",
		VideoProfile: "high",
		PixelFormat:  "yuv420p",
		Width:        a.profile.Width,
		Height:       a.profile.Height,
		FPSNum:       int(plan.FPSNumerator),
		FPSDen:       int(plan.FPSDenominator),
		AudioCodec:   a.profile.AudioCodec,
		SampleRate:   a.profile.SampleRate,
		Channels:     a.profile.Channels,
	}

	a.logPhase("compile_start", plan.Revision,
		zap.String("source_asset_id", src.AssetID),
		zap.String("source_path", src.Path),
		zap.Bool("has_subtitle", sub != nil),
		zap.Bool("has_watermark", opts.Watermark != nil),
		zap.Bool("has_watermark_spec", opts.WatermarkSpec != nil),
		zap.String("watermark_text", opts.WatermarkText),
		zap.String("background_mode", opts.BackgroundMode),
		zap.Bool("has_subtitle_style", opts.SubtitlesStyle != nil),
		zap.Int("width", contract.Width),
		zap.Int("height", contract.Height),
		zap.Int("fps_num", contract.FPSNum),
		zap.Int("fps_den", contract.FPSDen),
		zap.String("video_codec", contract.VideoCodec),
		zap.String("audio_codec", contract.AudioCodec),
	)
	compileStart := time.Now()
	clipPlan, err := cliprender.Compile(cliprender.CompileInput{
		RunID:         plan.Revision,
		DurationMS:    plan.Timeline.DurationUS / 1000,
		Source:        &cliprender.MaterializedAsset{AssetID: src.AssetID, LocalPath: src.Path, SHA256: src.SHA256},
		Watermark:     opts.Watermark,
		WatermarkSpec: opts.WatermarkSpec,
		WatermarkText: func() string {
			if opts.WatermarkSpec == nil {
				return ""
			}
			return opts.WatermarkSpec.Text
		}(),
		Background:             opts.Background,
		BackgroundMode:         opts.BackgroundMode,
		ForegroundScalePercent: opts.ForegroundScalePercent,
		Subtitles:              sub,
		SubtitlesStyle:         opts.SubtitlesStyle,
		Contract:               contract,
		AudioMode:              cliprender.AudioModeCopyIfCompatible,
		OutputPath:             plan.OutputPath,
	})
	compileMS := time.Since(compileStart).Milliseconds()
	if err != nil {
		a.logPhase("compile_failed", plan.Revision, zap.Int64("duration_ms", compileMS), zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: compile clip render plan: %w", err)
	}
	a.logPhase("compile_done", plan.Revision,
		zap.Int64("duration_ms", compileMS),
		zap.String("output_path", clipPlan.OutputPath),
		zap.String("plan_sha256", clipPlan.PlanSHA256),
	)

	renderStart := time.Now()
	a.logPhase("render_start", plan.Revision,
		zap.String("output_path", clipPlan.OutputPath),
	)
	outcome, err := a.renderer.Render(ctx, clipPlan)
	renderMS := time.Since(renderStart).Milliseconds()
	if err != nil {
		a.logPhase("render_failed", plan.Revision, zap.Int64("duration_ms", renderMS), zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: execute clip render: %w", err)
	}
	if outcome == nil || outcome.OutputPath == "" || outcome.SizeBytes <= 0 {
		a.logPhase("render_invalid_outcome", plan.Revision,
			zap.Int64("duration_ms", renderMS),
			zap.Any("outcome", outcome),
		)
		return localization.RenderFacts{}, fmt.Errorf("localization: clip render returned an invalid outcome")
	}
	a.logPhase("render_done", plan.Revision,
		zap.Int64("duration_ms", renderMS),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("size_bytes", outcome.SizeBytes),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.String("render_output_path", outcome.OutputPath),
	)

	hashStart := time.Now()
	sha, _, err := digest.SHA256File(outcome.OutputPath)
	if err != nil {
		a.logPhase("hash_failed", plan.Revision, zap.Int64("duration_ms", time.Since(hashStart).Milliseconds()), zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: hash rendered output: %w", err)
	}
	hashMS := time.Since(hashStart).Milliseconds()
	a.logPhase("hash_done", plan.Revision,
		zap.Int64("duration_ms", hashMS),
		zap.String("sha256", sha),
	)
	totalMS := compileMS + renderMS + hashMS
	a.log.Info("clip.render.localization.completed",
		zap.String("subsystem", "localization_render"),
		zap.String("plan_revision", plan.Revision),
		zap.String("output_path", outcome.OutputPath),
		zap.Int64("size_bytes", outcome.SizeBytes),
		zap.String("sha256", sha),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("compile_ms", compileMS),
		zap.Int64("render_ms", renderMS),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.Int64("hash_ms", hashMS),
		zap.Int64("total_ms", totalMS),
	)

	return localization.RenderFacts{
		LocalPath:  outcome.OutputPath,
		SHA256:     sha,
		SizeBytes:  outcome.SizeBytes,
		DurationMS: int64(outcome.DurationSec * 1000),
		VideoCodec: contract.VideoCodec,
		AudioCodec: contract.AudioCodec,
		Backend:    string(outcome.Backend),
		Metrics:    metricsMap(outcome.Metrics),
	}, nil
}

// metricsMap is a compatibility projection of the canonical V2 report for
// the localized artifact wire contract. NOT_INSTRUMENTED fields are omitted;
// measured numeric fields are preserved without inventing zeroes.
func metricsMap(m *cliprender.RenderMetricsV2) map[string]float64 {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	out := make(map[string]float64)
	for key, value := range raw {
		if number, ok := value.(float64); ok {
			out[key] = number
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

package app

// localization_render.go wires the localization capability's RenderPlanExecutor
// port to the canonical clip.render render_clip boundary — the only Rust
// operation that burns ASS subtitles. The sealed render.RenderPlan carries the
// timeline/identity (compiled by LocalizedClipCompiler); this adapter maps it
// plus the compiled subtitle ASS into the concrete ClipRenderPlanV1 and
// delegates to the already-wired RenderExecutor.
//
// godlike/06 SSOT: the adapter makes zero business selections and never
// invokes FFmpeg/ffprobe. The output contract (codec/pixel/geometry) is
// pinned by the composition-root media profile; the render executor + Rust
// re-audit the plan and every referenced artifact fail-closed.
//
// Composition-root invariant: the VideoProfile wired here MUST be the same
// profile the plan's OutputProfileHash fingerprints (the compiler folds
// OutputProfileHash into the ExecutionPolicy.TargetProfileHash). Wiring a
// different profile would render bytes that disagree with the plan's identity.
//
// Every boundary phase (validate, compile, render, hash) emits structured
// zap logs with per-phase timing so the localization path is reconstructible
// from server logs alone.

import (
	"context"
	"fmt"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"go.uber.org/zap"
)

// localizationRenderPlanExecutor implements localization.RenderPlanExecutor.
// It is fail-closed: an unwired render executor, an invalid plan, or a missing
// source/subtitle artifact is a typed error before any Rust process starts.
type localizationRenderPlanExecutor struct {
	renderer cliprender.RenderExecutor
	profile  mediaexec.VideoProfile
	log      *zap.Logger
}

// newLocalizationRenderPlanExecutor builds the bridge. profile is normalized
// to defaults so a partially-populated composition-root profile is safe.
// log is required so every phase of the execute pipeline is observable.
func newLocalizationRenderPlanExecutor(renderer cliprender.RenderExecutor, profile mediaexec.VideoProfile, log *zap.Logger) *localizationRenderPlanExecutor {
	if log == nil {
		log = zap.NewNop()
	}
	return &localizationRenderPlanExecutor{renderer: renderer, profile: profile.WithDefaults(), log: log}
}

var _ localization.RenderPlanExecutor = (*localizationRenderPlanExecutor)(nil)

func (a *localizationRenderPlanExecutor) logPhase(phase, planID string, fields ...zap.Field) {
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
// the output contract (Rust re-audits these before reporting success).
func (a *localizationRenderPlanExecutor) Execute(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset) (localization.RenderFacts, error) {
	return a.execute(ctx, plan, subtitle, nil, nil)
}

// ExecuteWithWatermark keeps watermarking on the same sealed render_clip
// invocation as subtitle burn. There is no second device/encode pass.
func (a *localizationRenderPlanExecutor) ExecuteWithWatermark(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset, watermark *cliprender.MaterializedAsset, spec *cliprender.WatermarkSpec) (localization.RenderFacts, error) {
	return a.execute(ctx, plan, subtitle, watermark, spec)
}

func (a *localizationRenderPlanExecutor) execute(ctx context.Context, plan render.RenderPlan, subtitle *localization.SubtitleAsset, watermark *cliprender.MaterializedAsset, watermarkSpec *cliprender.WatermarkSpec) (localization.RenderFacts, error) {
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
		zap.Bool("has_watermark", watermark != nil),
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
		Source:        &cliprender.MaterializedAsset{AssetID: src.AssetID, LocalPath: src.Path, SHA256: src.SHA256},
		Watermark:     watermark,
		WatermarkSpec: watermarkSpec,
		WatermarkText: func() string {
			if watermarkSpec == nil {
				return ""
			}
			return watermarkSpec.Text
		}(),
		Subtitles:  sub,
		Contract:   contract,
		AudioMode:  cliprender.AudioModeCopyIfCompatible,
		OutputPath: plan.OutputPath,
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
	sha, err := files.SHA256File(outcome.OutputPath)
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
	}, nil
}

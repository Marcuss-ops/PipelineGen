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

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// localizationRenderPlanExecutor implements localization.RenderPlanExecutor.
// It is fail-closed: an unwired render executor, an invalid plan, or a missing
// source/subtitle artifact is a typed error before any Rust process starts.
type localizationRenderPlanExecutor struct {
	renderer cliprender.RenderExecutor
	profile  mediaexec.VideoProfile
}

// newLocalizationRenderPlanExecutor builds the bridge. profile is normalized
// to defaults so a partially-populated composition-root profile is safe.
func newLocalizationRenderPlanExecutor(renderer cliprender.RenderExecutor, profile mediaexec.VideoProfile) *localizationRenderPlanExecutor {
	return &localizationRenderPlanExecutor{renderer: renderer, profile: profile.WithDefaults()}
}

var _ localization.RenderPlanExecutor = (*localizationRenderPlanExecutor)(nil)

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
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan validation failed: %w", err)
	}
	if len(plan.Manifest) == 0 {
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan has no source manifest entry")
	}
	src := plan.Manifest[0]
	if src.Path == "" || src.SHA256 == "" {
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan source is incomplete")
	}

	var sub *cliprender.SubtitleArtifact
	if subtitle != nil {
		if subtitle.LocalPath == "" || subtitle.SHA256 == "" {
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
		ContractID:   cliprender.OutputContractVeloxEditingClipV1,
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
	if err != nil {
		return localization.RenderFacts{}, fmt.Errorf("localization: compile clip render plan: %w", err)
	}

	outcome, err := a.renderer.Render(ctx, clipPlan)
	if err != nil {
		return localization.RenderFacts{}, fmt.Errorf("localization: execute clip render: %w", err)
	}
	if outcome == nil || outcome.OutputPath == "" || outcome.SizeBytes <= 0 {
		return localization.RenderFacts{}, fmt.Errorf("localization: clip render returned an invalid outcome")
	}

	sha, err := files.SHA256File(outcome.OutputPath)
	if err != nil {
		return localization.RenderFacts{}, fmt.Errorf("localization: hash rendered output: %w", err)
	}

	return localization.RenderFacts{
		LocalPath:  outcome.OutputPath,
		SHA256:     sha,
		SizeBytes:  outcome.SizeBytes,
		DurationMS: int64(outcome.DurationSec * 1000),
		VideoCodec: contract.VideoCodec,
		AudioCodec: contract.AudioCodec,
	}, nil
}

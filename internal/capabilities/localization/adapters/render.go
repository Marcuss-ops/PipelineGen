package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// resolveOverlays seals a plan's REUSED overlay lineages into the compile
// input, one per certified overlay.render artifact.
//
// The lineages are language-independent, so every variant of a source passes
// the same render_keys here and the resolver returns the same cached segments —
// one overlay render per item serves the whole multi-language fan-out. The
// resolver memoizes the segment digest, so N languages hash each overlay byte
// once instead of once per language.
//
// Fail-closed: an incomplete lineage, an unwired resolver, or an unresolvable
// segment (unknown render_key, unreadable artifact) is a typed error, so a
// render never ships a clip that silently lost an overlay it declared. Every
// declared lineage is resolved or the render does not start.
func (a *RenderPlanExecutor) resolveOverlays(ctx context.Context, lineages []cliprender.OverlayRefSpec) (*cliprender.PlanOverlayInput, error) {
	if len(lineages) == 0 {
		return nil, nil
	}
	segments := make([]cliprender.PlanOverlayInputSegment, 0, len(lineages))
	for i, lineage := range lineages {
		if strings.TrimSpace(lineage.RenderJobID) == "" ||
			strings.TrimSpace(lineage.PlanFingerprint) == "" ||
			strings.TrimSpace(lineage.RenderKey) == "" ||
			strings.TrimSpace(lineage.SourceVideoAssetID) == "" {
			return nil, fmt.Errorf("localization: overlay %d lineage is incomplete (render_job_id, plan_fingerprint, render_key and source_video_asset_id are required)", i)
		}
		if lineage.StartUS < 0 || lineage.EndUS <= lineage.StartUS {
			return nil, fmt.Errorf("localization: overlay %d window is invalid (end_us %d must be > start_us %d >= 0)", i, lineage.EndUS, lineage.StartUS)
		}
		if a.overlayResolver == nil {
			return nil, fmt.Errorf("localization: overlay %q requested but no overlay segment resolver is wired", lineage.RenderKey)
		}
		segment, err := a.overlayResolver.Resolve(ctx, cliprender.OverlayResolveInput{
			RenderJobID: lineage.RenderJobID,
			RenderKey:   lineage.RenderKey,
		})
		if err != nil {
			return nil, fmt.Errorf("localization: resolve reused overlay %q: %w", lineage.RenderKey, err)
		}
		if segment == nil || strings.TrimSpace(segment.LocalPath) == "" || strings.TrimSpace(segment.SHA256) == "" {
			return nil, fmt.Errorf("localization: resolved overlay %q is incomplete", lineage.RenderKey)
		}
		segments = append(segments, cliprender.PlanOverlayInputSegment{
			Segment: segment,
			StartMS: lineage.StartUS / 1000,
			EndMS:   lineage.EndUS / 1000,
		})
	}
	return &cliprender.PlanOverlayInput{Segments: segments}, nil
}

// certifiedOutputDigest resolves the content address of a rendered localized
// clip WITHOUT re-reading bytes the render boundary already certified.
//
// The RenderingGen boundary computes the digest of the exact bytes it wrote
// while streaming the certified download (queue_client.materializeArtifact
// hashes in the same io.Copy that produced the file and verifies size + digest
// against the queue's expected values), so RenderOutcome.SHA256 is the
// certified identity of OutputPath. `digest.SHA256File` here was therefore a
// second full pass over every localized clip for a fact already proven — the
// exact waste the clip.render completion path already removed by forwarding
// outcome.SHA256 straight to the publisher.
//
// Fail-closed: an outcome with no certified digest (a legacy or test renderer),
// or one whose certified size no longer matches the file on disk, is hashed
// from the real bytes instead of being trusted. certified reports which branch
// ran so the choice is observable on the phase log.
func certifiedOutputDigest(outcome *cliprender.RenderOutcome) (digestValue string, certified bool, err error) {
	if outcome == nil {
		return "", false, errors.New("render outcome is nil")
	}
	if outcome.SHA256 != "" {
		info, statErr := os.Stat(outcome.OutputPath)
		if statErr != nil {
			return "", false, statErr
		}
		if outcome.SizeBytes > 0 && info.Size() == outcome.SizeBytes {
			return outcome.SHA256, true, nil
		}
	}
	sha, _, hashErr := digest.SHA256File(outcome.OutputPath)
	if hashErr != nil {
		return "", false, hashErr
	}
	return sha, false, nil
}

// RenderPlanExecutor implements localization.RenderPlanExecutor.
// It is fail-closed: an unwired render executor, an invalid plan, or a missing
// source/subtitle artifact is a typed error before the Chronon render starts.
type RenderPlanExecutor struct {
	renderer cliprender.RenderExecutor
	profile  mediaexec.VideoProfile
	log      *zap.Logger
	// overlayResolver resolves the certified rendered overlay segments named by
	// a plan's overlay lineages, one per semantic overlay item. It is the
	// content-addressed hop that makes the reuse real: every language variant of
	// a source resolves the SAME render_keys, and the resolver memoizes each
	// segment digest, so N languages hash each overlay's bytes once and none of
	// them re-renders an overlay. Nil is allowed only while no plan carries an
	// overlay — a plan that does is a typed error, never a silent drop.
	overlayResolver cliprender.OverlaySegmentResolver
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

// logPhase emits the per-phase progress trace of the client-side
// validate/compile/render/hash window at Debug level. The hot path keeps only
// the canonical lifecycle events at Info (completed) and Warn (failed): the
// ten-ish per-phase lines this executor used to emit for EVERY localized clip
// were diagnostics, not operator events — the same facts already live on the
// RunReport stages and the render metrics.
// WithOverlayResolver wires the overlay-segment resolver used to seal a plan's
// reused overlay into the clip render plan. Required as soon as any plan
// declares an overlay; returns the receiver so composition roots can chain it.
func (a *RenderPlanExecutor) WithOverlayResolver(resolver cliprender.OverlaySegmentResolver) *RenderPlanExecutor {
	if a != nil {
		a.overlayResolver = resolver
	}
	return a
}

func (a *RenderPlanExecutor) logPhase(phase, planID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "localization_render"),
		zap.String("phase", phase),
		zap.String("plan_revision", planID),
	}, fields...)
	a.log.Debug("clip.render.localization.phase", all...)
}

// logPhaseFailure is the canonical FAILED event for a localized render: exactly
// one Warn per failed clip, carrying the phase that failed and its cause,
// instead of scattering the failure across Info-level progress lines that an
// operator had to join by hand.
func (a *RenderPlanExecutor) logPhaseFailure(phase, planID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "localization_render"),
		zap.String("phase", phase),
		zap.String("plan_revision", planID),
	}, fields...)
	a.log.Warn("clip.render.localization.failed", all...)
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
		a.logPhaseFailure("validate_failed", plan.Revision, zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan validation failed: %w", err)
	}
	if len(plan.Manifest) == 0 {
		a.logPhaseFailure("validate_failed", plan.Revision, zap.String("reason", "empty_manifest"))
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan has no source manifest entry")
	}
	src := plan.Manifest[0]
	if src.Path == "" || src.SHA256 == "" {
		a.logPhaseFailure("validate_failed", plan.Revision, zap.String("reason", "incomplete_source"))
		return localization.RenderFacts{}, fmt.Errorf("localization: render plan source is incomplete")
	}

	var sub *cliprender.SubtitleArtifact
	if subtitle != nil {
		if subtitle.LocalPath == "" || subtitle.SHA256 == "" {
			a.logPhaseFailure("validate_failed", plan.Revision, zap.String("reason", "incomplete_subtitle"))
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

	overlay, err := a.resolveOverlays(ctx, opts.Overlays)
	if err != nil {
		a.logPhaseFailure("overlay_resolve_failed", plan.Revision, zap.Error(err))
		return localization.RenderFacts{}, err
	}

	a.logPhase("compile_start", plan.Revision,
		zap.String("source_asset_id", src.AssetID),
		zap.String("source_path", src.Path),
		zap.Bool("has_subtitle", sub != nil),
		zap.Bool("has_watermark", opts.Watermark != nil),
		zap.Bool("has_watermark_spec", opts.WatermarkSpec != nil),
		zap.String("watermark_text", func() string {
			if opts.WatermarkSpec == nil {
				return ""
			}
			return opts.WatermarkSpec.Text
		}()),
		zap.String("background_mode", opts.BackgroundMode),
		zap.Bool("has_overlay", overlay != nil),
		zap.Int("overlay_segments_resolved", func() int {
			if overlay == nil {
				return 0
			}
			return len(overlay.Segments)
		}()),
		zap.Int("overlay_segments_declared", len(opts.Overlays)),
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
		RunID:                  plan.Revision,
		DurationMS:             plan.Timeline.DurationUS / 1000,
		Source:                 &cliprender.MaterializedAsset{AssetID: src.AssetID, LocalPath: src.Path, SHA256: src.SHA256},
		Watermark:              opts.Watermark,
		WatermarkSpec:          opts.WatermarkSpec,
		Background:             opts.Background,
		BackgroundMode:         opts.BackgroundMode,
		ForegroundScalePercent: opts.ForegroundScalePercent,
		Subtitles:              sub,
		SubtitlesStyle:         opts.SubtitlesStyle,
		Contract:               contract,
		AudioMode:              cliprender.AudioModeCopyIfCompatible,
		Overlay:                overlay,
		OutputPath:             plan.OutputPath,
	})
	compileMS := time.Since(compileStart).Milliseconds()
	if err != nil {
		a.logPhaseFailure("compile_failed", plan.Revision, zap.Int64("duration_ms", compileMS), zap.Error(err))
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
	// Drive the same Submit/Settle continuation every other caller uses. The
	// blocking Render form was DELETED in the 2026-09-13 audit: localized
	// renders are driven from the client-side scheduler, so the two halves are
	// adjacent here, but there is now exactly ONE way to drive a render (and
	// exactly one place that can release a worker slot while RenderingGen runs).
	if submitErr := a.renderer.Submit(ctx, clipPlan); submitErr != nil {
		submitMS := time.Since(renderStart).Milliseconds()
		a.logPhaseFailure("render_submit_failed", plan.Revision, zap.Int64("duration_ms", submitMS), zap.Error(submitErr))
		return localization.RenderFacts{}, fmt.Errorf("localization: submit clip render: %w", submitErr)
	}
	outcome, err := a.renderer.Settle(ctx, clipPlan)
	renderMS := time.Since(renderStart).Milliseconds()
	if err != nil {
		a.logPhaseFailure("render_failed", plan.Revision, zap.Int64("duration_ms", renderMS), zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: execute clip render: %w", err)
	}
	if outcome == nil || outcome.SizeBytes <= 0 {
		a.logPhaseFailure("render_invalid_outcome", plan.Revision,
			zap.Int64("duration_ms", renderMS),
			zap.Any("outcome", outcome),
		)
		return localization.RenderFacts{}, fmt.Errorf("localization: clip render returned an invalid outcome")
	}
	// Locator-first boundary: Settle no longer writes the artifact locally, but
	// localization genuinely needs the bytes (it publishes the localized file
	// and hashes it). Materialize on demand through the boundary, which
	// verifies size + certified digest while streaming. Fail-closed: a
	// locator-only outcome from a renderer that cannot materialize is an error.
	if strings.TrimSpace(outcome.OutputPath) == "" {
		materializer, ok := a.renderer.(cliprender.RenderArtifactMaterializer)
		if !ok {
			a.logPhaseFailure("materialize_unavailable", plan.Revision, zap.String("reason", "locator_only_outcome"))
			return localization.RenderFacts{}, fmt.Errorf("localization: render returned a locator-only outcome but the renderer cannot materialize it")
		}
		materializeStart := time.Now()
		if _, mErr := materializer.Materialize(ctx, outcome, plan.OutputPath); mErr != nil {
			a.logPhaseFailure("materialize_failed", plan.Revision, zap.Int64("duration_ms", time.Since(materializeStart).Milliseconds()), zap.Error(mErr))
			return localization.RenderFacts{}, fmt.Errorf("localization: materialize rendered output: %w", mErr)
		}
		a.logPhase("materialize_done", plan.Revision,
			zap.Int64("duration_ms", time.Since(materializeStart).Milliseconds()),
			zap.String("output_path", outcome.OutputPath))
	}
	a.logPhase("render_done", plan.Revision,
		zap.Int64("duration_ms", renderMS),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("size_bytes", outcome.SizeBytes),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.String("render_output_path", outcome.OutputPath),
	)

	hashStart := time.Now()
	sha, certifiedDigest, err := certifiedOutputDigest(outcome)
	if err != nil {
		a.logPhaseFailure("hash_failed", plan.Revision, zap.Int64("duration_ms", time.Since(hashStart).Milliseconds()), zap.Error(err))
		return localization.RenderFacts{}, fmt.Errorf("localization: hash rendered output: %w", err)
	}
	hashMS := time.Since(hashStart).Milliseconds()
	a.logPhase("hash_done", plan.Revision,
		zap.Int64("duration_ms", hashMS),
		zap.String("sha256", sha),
		zap.Bool("certified_digest_reused", certifiedDigest),
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
		zap.Bool("certified_digest_reused", certifiedDigest),
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

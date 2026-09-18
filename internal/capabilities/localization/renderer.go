package localization

// renderer.go owns the canonical render step of the localization fan-out: the
// seam where a validated LocalizedClipPlan is compiled into the sealed
// deterministic RenderPlan, the translated subtitle track is wired into a
// deterministic ASS, and the Rust render boundary produces the certified local
// bytes — returned as a LocalizedClipArtifact in the RENDERED state.
//
// Pipeline:
//
//	LocalizedClipPlan
//	  ├─ Compiler         → render.RenderPlan (sealed + validated)
//	  ├─ SubtitleWire     → SubtitleAsset (.ass, hash-verified)
//	  └─ RenderPlanExecutor → RenderFacts (RenderingGen → Chronon3D boundary)
//	  → LocalizedClipArtifact{Status: RENDERED}
//
// godlike/06 SSOT (one canonical owner per fact): the renderer makes ZERO
// business selections and never invokes FFmpeg/ffprobe. It delegates to the
// existing deterministic architecture (compiler → render.RenderPlan) and a
// narrow executor port; the composition root wires the Rust boundary. There
// is no ad-hoc FFmpeg path — a localized render flows through the same sealed
// RenderPlan + executor as every other render.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// RenderFacts is the certified outcome of one localized render: the local
// bytes + the media facts the artifact carries. Every field is observed by
// the render boundary (the executor reads the actual output file and reports
// codecs/duration); the renderer never re-derives them.
type RenderFacts struct {
	LocalPath  string
	SHA256     string
	SizeBytes  int64
	DurationMS int64
	VideoCodec string
	AudioCodec string
	Backend    string
	Metrics    map[string]float64
}

// RenderPlanExecutor executes a sealed render.RenderPlan together with the
// burned subtitle ASS into certified local bytes. The concrete adapter drives
// the RenderingGen → Chronon3D boundary (the only operation that burns ASS
// subtitles); the capability never invokes FFmpeg/ffprobe itself.
type RenderPlanExecutor interface {
	Execute(ctx context.Context, plan render.RenderPlan, subtitle *SubtitleAsset) (RenderFacts, error)
}

// RenderOptions carries every visual layer the executor must fold into the
// sealed ClipRenderPlanV1: watermark (materialized asset + spec + text),
// background (mode + materialized asset for mode=asset), and the subtitle
// visual overrides. All fields are optional; nil/empty means "no such layer".
type RenderOptions struct {
	Watermark              *cliprender.MaterializedAsset
	WatermarkSpec          *cliprender.WatermarkSpec
	Background             *cliprender.MaterializedAsset
	BackgroundMode         string
	BackgroundKind         string
	ForegroundScalePercent int
	SubtitlesStyle         *scriptpkg.VideoVisualStyleSpec
	// Overlays carries the reused entity overlay lineages this variant
	// composites in the SAME render pass, one per certified overlay.render
	// artifact (a scene's phrase, entity card and keyword are three segments).
	// They are shared by every language variant of a source
	// (language-independent), so each segment is resolved/hashed once and every
	// language reuses those bytes instead of re-rendering the overlay.
	Overlays []cliprender.OverlayRefSpec
}

// ExtendedRenderPlanExecutor is the full-fidelity executor implemented by
// the production clip-render bridge: every visual layer of the plan reaches
// the sealed plan without loss.
type ExtendedRenderPlanExecutor interface {
	RenderPlanExecutor
	ExecuteExtended(ctx context.Context, plan render.RenderPlan, subtitle *SubtitleAsset, opts RenderOptions) (RenderFacts, error)
}

// WatermarkRenderPlanExecutor is the watermark-only executor, kept
// source-compatible with existing tests and non-watermarked callers. New
// callers should prefer ExtendedRenderPlanExecutor so background + subtitle
// style propagate too.
type WatermarkRenderPlanExecutor interface {
	RenderPlanExecutor
	ExecuteWithWatermark(ctx context.Context, plan render.RenderPlan, subtitle *SubtitleAsset, watermark *cliprender.MaterializedAsset, spec *cliprender.WatermarkSpec) (RenderFacts, error)
}

// LocalizedClipRenderer is the canonical render step. It is immutable after
// construction and safe for concurrent Render calls (the scheduler fans out
// one Render per language).
type LocalizedClipRenderer struct {
	compiler Compiler
	wire     *SubtitleWire
	executor RenderPlanExecutor
	// reuseCache is the optional content-addressed reuse seam (render_reuse.go).
	// Nil means "always render", which is the pre-existing behaviour.
	reuseCache RenderReuseCache
}

// NewLocalizedClipRenderer builds the renderer. Fail-closed: all three
// dependencies are mandatory — a renderer that cannot compile, wire
// subtitles, or execute can never produce a certified artifact.
func NewLocalizedClipRenderer(compiler Compiler, wire *SubtitleWire, executor RenderPlanExecutor) (*LocalizedClipRenderer, error) {
	if compiler == nil {
		return nil, fmt.Errorf("localization.NewLocalizedClipRenderer: compiler is required")
	}
	if wire == nil {
		return nil, fmt.Errorf("localization.NewLocalizedClipRenderer: subtitle wire is required")
	}
	if executor == nil {
		return nil, fmt.Errorf("localization.NewLocalizedClipRenderer: render executor is required")
	}
	return &LocalizedClipRenderer{compiler: compiler, wire: wire, executor: executor}, nil
}

// Render compiles the plan, wires the subtitle ASS, executes the render via
// the RenderingGen → Chronon3D boundary, and returns the certified RENDERED artifact. Fail-closed:
// an invalid plan, a compile/wire/execute failure, or incomplete render facts
// all abort before any artifact is produced.
//
// Render matches the scheduler's RenderFunc signature, so the canonical
// fan-out wires it directly: NewScheduler(ctx, renderer.Render, concurrency).
func (r *LocalizedClipRenderer) Render(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
	if r == nil || r.compiler == nil || r.wire == nil || r.executor == nil {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: renderer is not initialized")
	}
	if err := plan.Validate(); err != nil {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: %w", err)
	}

	// 1. Deterministic render contract (sealed + validated by render.Compile).
	renderPlan, err := r.compiler.Compile(ctx, plan)
	if err != nil {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: compile: %w", err)
	}

	// 2. Translated subtitle ASS, hash-verified against plan.SubtitleSHA256.
	ass, err := r.wire.Wire(ctx, plan)
	if err != nil {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: subtitle wire: %w", err)
	}

	// 3. Content-addressed reuse (render_reuse.go). Checked AFTER the subtitle
	// wire on purpose: a reuse hit must still resolve and verify THIS language's
	// translated track, so cached bytes can never carry a stale or wrong-language
	// subtitle artifact. Only the GPU half (compile/submit/settle/probe) is
	// skipped, and only against bytes that hash to the certified digest.
	if reused, ok := r.reusedArtifact(ctx, plan, ass); ok {
		return reused, nil
	}

	// 4. Chronon render boundary.
	var facts RenderFacts
	opts := RenderOptions{
		Watermark:              plan.Watermark,
		WatermarkSpec:          plan.WatermarkSpec,
		Background:             plan.Background,
		BackgroundMode:         plan.BackgroundMode,
		BackgroundKind:         plan.BackgroundKind,
		ForegroundScalePercent: plan.ForegroundScalePercent,
		SubtitlesStyle:         plan.SubtitlesStyle,
		Overlays:               plan.Overlays,
	}
	// hasVisual selects the full-fidelity executor. The overlays belong in this
	// set: an overlay-only clip (no watermark, no background) would otherwise
	// take the plain Execute branch and drop the reused overlays silently.
	hasVisual := plan.Watermark != nil ||
		(plan.WatermarkSpec != nil && strings.TrimSpace(plan.WatermarkSpec.Text) != "") ||
		plan.Background != nil || plan.BackgroundMode != "" || plan.ForegroundScalePercent > 0 ||
		len(plan.Overlays) > 0
	if extended, ok := r.executor.(ExtendedRenderPlanExecutor); ok && hasVisual {
		// Full fidelity: background + subtitle style ride the same sealed
		// render_clip invocation as the watermark (no second pass).
		facts, err = extended.ExecuteExtended(ctx, renderPlan, ass, opts)
	} else if wm, ok := r.executor.(WatermarkRenderPlanExecutor); ok &&
		(plan.Watermark != nil || (plan.WatermarkSpec != nil && strings.TrimSpace(plan.WatermarkSpec.Text) != "")) {
		// Watermark-only path (legacy executors): the sealed WatermarkSpec
		// still reaches the renderer so text overlays are never dropped.
		facts, err = wm.ExecuteWithWatermark(ctx, renderPlan, ass, plan.Watermark, plan.WatermarkSpec)
	} else {
		facts, err = r.executor.Execute(ctx, renderPlan, ass)
	}
	if err != nil {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: execute: %w", err)
	}

	// 5. Fail-closed: the certified facts must be complete (godlike/07 — an
	// artifact is never RENDERED without verified bytes + media facts).
	if facts.LocalPath == "" || !isSHA256Hex(facts.SHA256) || facts.SizeBytes <= 0 || facts.DurationMS <= 0 {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: executor returned incomplete render facts")
	}

	// Record the certified bytes so the next identical plan skips this render
	// (fail-soft: a cache write failure never fails the artifact, see
	// render_reuse.go).
	r.storeRendered(ctx, plan, facts)

	return LocalizedClipArtifact{
		Version:         LocalizedClipArtifactVersion,
		JobID:           plan.JobID,
		SceneID:         plan.SceneID,
		ClipID:          plan.ClipID,
		Language:        plan.TargetLanguage,
		PlanFingerprint: plan.Fingerprint,
		LocalPath:       facts.LocalPath,
		SubtitlePath:    ass.LocalPath,
		SubtitleSHA256:  ass.SHA256,
		SHA256:          facts.SHA256,
		SizeBytes:       facts.SizeBytes,
		DurationMS:      facts.DurationMS,
		VideoCodec:      facts.VideoCodec,
		AudioCodec:      facts.AudioCodec,
		Backend:         facts.Backend,
		MetricsJSON:     metricsJSON(facts.Metrics),
		Status:          LocalizedClipRendered,
	}, nil
}

func metricsJSON(metrics map[string]float64) string {
	if len(metrics) == 0 {
		return ""
	}
	b, err := json.Marshal(metrics)
	if err != nil {
		return ""
	}
	return string(b)
}

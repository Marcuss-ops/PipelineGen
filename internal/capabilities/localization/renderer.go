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
//	  └─ RenderPlanExecutor → RenderFacts (Rust render boundary)
//	  → LocalizedClipArtifact{Status: RENDERED}
//
// godlike/06 SSOT (one canonical owner per fact): the renderer makes ZERO
// business selections and never invokes FFmpeg/ffprobe. It delegates to the
// existing deterministic architecture (compiler → render.RenderPlan) and a
// narrow executor port; the composition root wires the Rust boundary. There
// is no ad-hoc FFmpeg path — a localized render flows through the same sealed
// RenderPlan + executor as every other render.

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
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
}

// RenderPlanExecutor executes a sealed render.RenderPlan together with the
// burned subtitle ASS into certified local bytes. The concrete adapter drives
// the Rust render boundary (the only operation that burns ASS subtitles); the
// capability never invokes FFmpeg/ffprobe itself.
type RenderPlanExecutor interface {
	Execute(ctx context.Context, plan render.RenderPlan, subtitle *SubtitleAsset) (RenderFacts, error)
}

// WatermarkRenderPlanExecutor is the extended executor implemented by the
// production clip-render bridge. The base interface remains source-compatible
// with existing tests and non-watermarked callers.
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
// the Rust boundary, and returns the certified RENDERED artifact. Fail-closed:
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

	// 3. Rust render boundary.
	var facts RenderFacts
	// Text watermarks do not require a materialized image asset. The executor
	// still needs the extended path so the sealed WatermarkSpec reaches the
	// renderer; checking only plan.Watermark silently dropped text overlays.
	if executor, ok := r.executor.(WatermarkRenderPlanExecutor); ok &&
		(plan.Watermark != nil || (plan.WatermarkSpec != nil && strings.TrimSpace(plan.WatermarkSpec.Text) != "")) {
		facts, err = executor.ExecuteWithWatermark(ctx, renderPlan, ass, plan.Watermark, plan.WatermarkSpec)
	} else {
		facts, err = r.executor.Execute(ctx, renderPlan, ass)
	}
	if err != nil {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: execute: %w", err)
	}

	// 4. Fail-closed: the certified facts must be complete (godlike/07 — an
	// artifact is never RENDERED without verified bytes + media facts).
	if facts.LocalPath == "" || !isSHA256Hex(facts.SHA256) || facts.SizeBytes <= 0 || facts.DurationMS <= 0 {
		return LocalizedClipArtifact{Status: LocalizedClipFailed}, fmt.Errorf("localization: render: executor returned incomplete render facts")
	}

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
		Status:          LocalizedClipRendered,
	}, nil
}

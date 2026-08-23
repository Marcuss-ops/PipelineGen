package localization

// compiler.go owns the canonical LocalizedClipPlan → render.RenderPlan
// compiler. It is the single seam between the localization contract and the
// existing deterministic render architecture:
//
//	LocalizedClipPlan
//	        ↓
//	LocalizedClipCompiler
//	        ↓
//	render.RenderPlan (sealed: ManifestSHA256 + PlanSHA256 + Validate)
//	        ↓
//	Rust executor
//
// godlike/06 SSOT (one canonical owner per fact): the compiler NEVER calls
// FFmpeg/ffprobe directly. It resolves the plan's references into concrete
// render facts through narrow ports, then delegates to render.Compile — so a
// localized render flows through the same sealed RenderPlan (ExecutionPolicy,
// Manifest, PlanSHA256, Validate, Seal) as every other render instead of
// bypassing the deterministic architecture with an ad-hoc ffmpeg invocation.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Compiler compiles a LocalizedClipPlan into the canonical sealed
// render.RenderPlan. The plan is the deterministic contract the Rust executor
// consumes; the compiler performs no rendering.
type Compiler interface {
	Compile(ctx context.Context, localized LocalizedClipPlan) (render.RenderPlan, error)
}

// SourceResolver resolves the source clip referenced by a LocalizedClipPlan
// into its local render facts (verified path + content hash + rational frame
// rate). The compiler never resolves paths itself and never runs ffprobe.
type SourceResolver interface {
	ResolveSource(ctx context.Context, assetID string) (SourceFacts, error)
}

// SourceFacts is the resolved source-clip identity the compiler needs to
// build the render plan. It carries no technology-specific types (no FFmpeg,
// no filesystem handle) — only the verified path, hash, duration, and frame
// rate.
type SourceFacts struct {
	AssetID    string
	LocalPath  string
	SHA256     string
	DurationMS int64
	FrameRate  audio.FrameRate
}

// CompilerConfig pins the deterministic, deployment-scoped render facts that
// the LocalizedClipPlan references by hash. It is shared by every localized
// render in one composition root — NOT per-plan.
type CompilerConfig struct {
	// WorkDir is the scratch root where the rendered output lands. Empty
	// yields a bare relative filename.
	WorkDir string
	// EncoderPolicyHash is the canonical 64-hex SHA-256 of the encoder policy
	// (preset / CRF / pixel format) applied to every localized render. It
	// enters the render ExecutionPolicy, so a policy change invalidates every
	// cached localized artifact.
	EncoderPolicyHash string
}

// LocalizedClipCompiler is the canonical Compiler implementation. It is
// immutable after construction and safe for concurrent Compile calls.
type LocalizedClipCompiler struct {
	sources SourceResolver
	config  CompilerConfig
}

// NewLocalizedClipCompiler builds the canonical compiler. Fail-closed: a nil
// source resolver is rejected at construction (a compiler that cannot resolve
// the source can never produce a valid plan).
func NewLocalizedClipCompiler(sources SourceResolver, config CompilerConfig) (*LocalizedClipCompiler, error) {
	if sources == nil {
		return nil, fmt.Errorf("localization.NewLocalizedClipCompiler: source resolver is required")
	}
	return &LocalizedClipCompiler{sources: sources, config: config}, nil
}

// Compile validates the plan (fail-closed), resolves the source, and produces
// a sealed render.RenderPlan via render.Compile. The returned plan is fully
// validated: its ManifestSHA256 and PlanSHA256 are computed by render.Seal and
// re-checked by render.Validate before it is returned.
func (c *LocalizedClipCompiler) Compile(ctx context.Context, localized LocalizedClipPlan) (render.RenderPlan, error) {
	if c == nil || c.sources == nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: compiler is not initialized")
	}
	if err := localized.Validate(); err != nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: %w", err)
	}

	src, err := c.sources.ResolveSource(ctx, localized.SourceAssetID)
	if err != nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: resolve source %s: %w", localized.SourceAssetID, err)
	}
	// godlike/07 fail-closed: the resolved bytes must match the plan's
	// SourceSHA256, or the render would read different bytes than the plan
	// fingerprints.
	if src.SHA256 != localized.SourceSHA256 {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: source sha256 mismatch for %s: resolved %q, plan %q", localized.SourceAssetID, src.SHA256, localized.SourceSHA256)
	}
	if err := src.FrameRate.Validate(); err != nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: invalid source frame rate: %w", err)
	}

	// A localized render plays the source clip in full: one timeline segment
	// covering the plan duration, one video source, silence as the (empty)
	// audio intent — the source's own audio is preserved by the render pass,
	// never re-synthesized here.
	durationUS := localized.DurationMS * 1000
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: durationUS,
		Segments: []audio.TimelineSegment{{
			ID:              localized.ClipID,
			Index:           0,
			TimelineStartUS: 0,
			DurationUS:      durationUS,
			Video: audio.VideoSegment{
				AssetID:          localized.SourceAssetID,
				SourceInUS:       0,
				SourceDurationUS: durationUS,
			},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}

	// The manifest frame count is the deterministic number of frames the
	// timeline occupies at the source frame rate — the source plays in full,
	// so the referenced source range is exactly [0, durationFrames).
	resolver, err := audio.NewFrameResolver(src.FrameRate)
	if err != nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: frame resolver: %w", err)
	}
	durationFrames, err := resolver.FrameCountForDuration(durationUS)
	if err != nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: duration frames: %w", err)
	}

	plan, err := render.Compile(render.CompileInput{
		JobID:      localized.JobID,
		Revision:   localizedRevision(localized),
		OutputPath: c.outputPath(localized),
		FrameRate:  src.FrameRate,
		Timeline:   timeline,
		Manifest: []render.AssetManifestEntry{{
			AssetID:    localized.SourceAssetID,
			Path:       src.LocalPath,
			SHA256:     src.SHA256,
			FrameCount: durationFrames,
		}},
		ExecutionPolicy: &render.RenderExecutionPolicy{
			// A localized clip always burns subtitles into the video, so the
			// source is never stream-copyable: it is re-encoded.
			AllowStreamCopy:   false,
			TargetProfileHash: canonicalSHA256(localized.OutputProfileHash),
			RendererVersion:   localized.RendererVersion,
			EncoderPolicyHash: c.config.EncoderPolicyHash,
		},
	})
	if err != nil {
		return render.RenderPlan{}, fmt.Errorf("localization: compile: %w", err)
	}
	return plan, nil
}

// outputPath derives the deterministic output path from the plan identity and
// target language, under the configured work dir.
func (c *LocalizedClipCompiler) outputPath(localized LocalizedClipPlan) string {
	name := localized.ClipID + "." + localized.TargetLanguage + ".mp4"
	if c != nil && strings.TrimSpace(c.config.WorkDir) != "" {
		return filepath.Join(c.config.WorkDir, name)
	}
	return name
}

// localizedRevision derives a deterministic, human-readable render revision
// from the plan identity. It is stable across re-runs of the same plan.
func localizedRevision(localized LocalizedClipPlan) string {
	return localized.ClipID + "/" + localized.TargetLanguage
}

// canonicalSHA256 folds an opaque profile/style hash string into a canonical
// 64-hex SHA-256, the shape render.RenderExecutionPolicy requires for its
// target-profile hash. Deterministic: the same string always folds to the
// same digest. Delegates to kernel/digest (SSOT).
func canonicalSHA256(s string) string {
	return digest.SHA256String(s)
}

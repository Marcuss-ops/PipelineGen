package app

// cliprender_plan.go wires the plan-facing adapters for the clip.render
// preparation phase:
//
//   - clipRenderSubtitleCompiler — deterministic ASS artifact (burn|sidecar)
//     via texttracks.CompileASSContent (the single owner of ASS content).
//   - clipRenderExecutorAdapter — maps the rustexec.ClipRenderer result onto
//     the capability's RenderOutcome verbatim (media facts come from the Rust
//     metadata, never re-derived).
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// ── SubtitleCompiler (deterministic ASS) ─────────────────────────────

// clipRenderSubtitleCompiler implements the capability's SubtitleCompiler
// port with the canonical ASS generator (texttracks.CompileASSContent — the
// single owner of ASS content generation). The artifact is written into the
// run's scratch dir (subtitles.ass) and validated before the plan is sealed.
//
// Determinism: identical cues + style ALWAYS produce identical bytes (the
// generator embeds no timestamps/randoms/paths). Mode burn|sidecar only
// tags the artifact — the ASS bytes are the same, the render pass decides
// whether to rasterize libass (burn) or ship the file (sidecar).
//
// Fail-closed: empty cues, an invalid mode, or an invalid generated ASS is a
// typed error — speech recognition is NEVER regenerated just to build
// subtitles (feature spec §5).
type clipRenderSubtitleCompiler struct{}

func (c *clipRenderSubtitleCompiler) Compile(ctx context.Context, in cliprender.SubtitleCompileInput) (*cliprender.SubtitleArtifact, error) {
	switch in.Mode {
	case cliprender.SubtitlesModeBurn, cliprender.SubtitlesModeSidecar:
	default:
		return nil, fmt.Errorf("%w: invalid subtitle mode %q", cliprender.ErrSubtitleCompileUnavailable, in.Mode)
	}
	if len(in.Cues) == 0 {
		return nil, fmt.Errorf("%w: zero cues for asset %q — subtitles cannot be compiled without transcript timing (speech recognition is never regenerated for subtitles)", cliprender.ErrSubtitleCompileUnavailable, in.AssetID)
	}
	content, err := texttracks.CompileASSContent(mapClipRenderCues(in.Cues), in.StyleID)
	if err != nil {
		return nil, fmt.Errorf("%w: compile ASS content: %v", cliprender.ErrSubtitleCompileUnavailable, err)
	}
	if err := os.MkdirAll(in.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create subtitle output dir %q: %w", in.OutputDir, err)
	}
	localPath := filepath.Join(in.OutputDir, "subtitles.ass")
	if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write ASS artifact %q: %w", localPath, err)
	}
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	if err := texttracks.ValidateASSFile(localPath, in.ClipDurationMS); err != nil {
		return nil, fmt.Errorf("%w: invalid generated ASS for asset %q: %v", cliprender.ErrSubtitleCompileUnavailable, in.AssetID, err)
	}
	return &cliprender.SubtitleArtifact{
		LocalPath: localPath,
		SHA256:    sha,
		Mode:      in.Mode,
		StyleID:   in.StyleID,
	}, nil
}

// ── RenderExecutor (Rust render_clip boundary) ───────────────────────

// clipRenderExecutor is the narrow seam over the rustexec.ClipRenderer so
// the composition-root adapter stays testable without the Rust process.
// The concrete implementation re-validates the sealed plan, verifies every
// referenced artifact, resolves the encoder policy from the composition-root
// media config, and refuses success without a non-empty output.
type clipRenderExecutor interface {
	RenderClip(ctx context.Context, plan cliprender.ClipRenderPlanV1) (rustexec.ClipRenderResult, error)
}

// clipRenderExecutorAdapter bridges the rustexec.ClipRenderer to the
// capability's RenderExecutor port. Fail-closed: an unwired renderer is a
// typed error (never a silent no-op that reports a rendered clip).
type clipRenderExecutorAdapter struct {
	renderer clipRenderExecutor
}

func (a *clipRenderExecutorAdapter) Render(ctx context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	if a == nil || a.renderer == nil {
		return nil, fmt.Errorf("%w: Rust clip renderer not wired", cliprender.ErrRenderPhaseNotImplemented)
	}
	result, err := a.renderer.RenderClip(ctx, plan)
	if err != nil {
		return nil, err
	}
	return &cliprender.RenderOutcome{
		OutputPath:        result.OutputPath,
		SizeBytes:         result.SizeBytes,
		DurationSec:       result.DurationSec,
		Width:             result.Width,
		Height:            result.Height,
		FPS:               result.FPS,
		FFmpegMS:          result.FFmpegMS,
		AudioCopyEligible: result.AudioCopyEligible,
		AudioEncodePasses: result.AudioEncodePasses,
		SubtitleRasterCPU: result.SubtitleRasterCPU,
	}, nil
}

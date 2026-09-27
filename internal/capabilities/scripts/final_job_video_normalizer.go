package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// normalizeFinalJobVideoBackground makes producer-local stock inputs safe for
// Chronon's native frame-zero decoder. Drive clips can have their first video
// sample at a small positive PTS (for example 21 ms); a composite starts at
// zero and the native decoder correctly rejects a request before that sample.
// Re-encode a silent, timestamp-zero copy for the intermediate composite only.
// The remote final job still receives the certified composite and its original
// separately compiled final audio.
func normalizeFinalJobVideoBackground(ctx context.Context, plan capoverlay.OverlayPlan) (capoverlay.OverlayPlan, func(), error) {
	if plan.Source == nil {
		return plan, nil, nil
	}
	source := *plan.Source
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return plan, nil, fmt.Errorf("ffmpeg is required to normalize stock timestamps: %w", err)
	}
	workDir, err := os.MkdirTemp("", "pipelinegen-final-composite-")
	if err != nil {
		return plan, nil, fmt.Errorf("create normalization workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	input := strings.TrimSpace(source.LocalPath)
	if input == "" {
		cleanup()
		return plan, nil, fmt.Errorf("source asset %q has no materialized local path", source.AssetID)
	}
	output := filepath.Join(workDir, "source.mp4")
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts",
		"-i", input, "-map", "0:v:0", "-an", "-vf", "setpts=PTS-STARTPTS",
		"-vsync", "0", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "20",
		"-pix_fmt", "yuv420p", "-avoid_negative_ts", "make_zero", "-movflags", "+faststart", output,
	)
	if combined, runErr := cmd.CombinedOutput(); runErr != nil {
		cleanup()
		return plan, nil, fmt.Errorf("normalize source asset %q: %w: %s", source.AssetID, runErr, strings.TrimSpace(string(combined)))
	}
	hash, size, hashErr := digest.SHA256File(output)
	if hashErr != nil {
		cleanup()
		return plan, nil, fmt.Errorf("hash normalized source asset %q: %w", source.AssetID, hashErr)
	}
	if size == 0 {
		cleanup()
		return plan, nil, fmt.Errorf("normalized source asset %q is empty", source.AssetID)
	}
	plan.Source = &capoverlay.OverlaySource{
		AssetID:   "finaljob-video-" + hash[:16],
		Path:      "assets/semantic/finaljob-video-" + hash + ".mp4",
		LocalPath: output, SHA256: hash,
	}
	return plan, cleanup, nil
}

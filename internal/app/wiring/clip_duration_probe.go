// Package wiring — clip_duration_probe.go owns the canonical clip probe port
// and the asset identity/duration resolution helpers used by the scene-text
// generator. All duration probing flows through the canonical media probe port
// (rustexec.VideoProcessor); this package never spawns ffprobe directly.
package wiring

import (
	"context"
	"fmt"
	"strings"

	mediaexec "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ClipProber is the canonical media probe port used to measure an asset's
// total duration when the catalog duration is absent. The concrete
// implementation is the Rust media prober (rustexec.VideoProcessor); the
// wiring layer must never spawn ffprobe directly.
type ClipProber interface {
	Probe(context.Context, string) (*mediaexec.MediaInfo, error)
}

func renderAssetSHA256(a *asset.Asset) (string, error) {
	if a == nil || strings.TrimSpace(a.LocalPath()) == "" {
		return "", fmt.Errorf("asset has no local path")
	}
	if candidate := strings.TrimSpace(a.LegacyFileMD5()); len(candidate) == 64 && !strings.Contains(candidate, ":") {
		return candidate, nil
	}
	return fileutil.SHA256File(a.LocalPath())
}

// renderAssetDurationSeconds returns the certified asset total duration. It
// prefers the catalog duration and only falls back to probing the local
// binary through the canonical media probe port (never a raw ffprobe exec).
func (g *SceneTextGenerator) renderAssetDurationSeconds(ctx context.Context, a *asset.Asset) (float64, error) {
	if a == nil {
		return 0, fmt.Errorf("asset is nil")
	}
	if a.Duration > 0 {
		return a.Duration.Seconds(), nil
	}
	path := strings.TrimSpace(a.LocalPath())
	if path == "" {
		return 0, fmt.Errorf("asset has no local execution source")
	}
	if g.Probe == nil {
		return 0, fmt.Errorf("asset duration probe is not configured")
	}
	info, err := g.Probe.Probe(ctx, path)
	if err != nil {
		return 0, fmt.Errorf("probe media duration: %w", err)
	}
	if info == nil || info.Duration <= 0 {
		return 0, fmt.Errorf("probe returned invalid duration")
	}
	return info.Duration.Seconds(), nil
}

package overlays

import (
	"context"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

var _ capoverlay.MediaProber = (*MediaContractProber)(nil)

// MediaContractProber adapts the canonical media probe capability
// (rustexec.VideoProcessor.Probe) into the overlay MediaProber port. It
// probes the rendered artifact with ffprobe through the canonical probe port
// (never a raw subprocess) and hashes the file, so the caller can validate
// the full OverlayMediaContract fail-closed before publication. The
// renderer's exit code is not a validity criterion here: only the probed
// facts + hash + size are.
type MediaContractProber struct {
	probe func(ctx context.Context, path string) (*mediaexec.MediaInfo, error)
}

// NewMediaContractProber constructs the prober around any canonical media
// probe implementation. A nil proc leaves the prober unconfigured, which
// ProbeOverlay reports as a typed error (fail closed), never a no-op.
func NewMediaContractProber(proc interface {
	Probe(context.Context, string) (*mediaexec.MediaInfo, error)
}) *MediaContractProber {
	p := &MediaContractProber{}
	if proc != nil {
		p.probe = proc.Probe
	}
	return p
}

func (p *MediaContractProber) ProbeOverlay(ctx context.Context, path string) (capoverlay.OverlayProbeResult, error) {
	if p == nil || p.probe == nil {
		return capoverlay.OverlayProbeResult{}, fmt.Errorf("overlay media prober is not configured")
	}
	info, err := p.probe(ctx, path)
	if err != nil {
		return capoverlay.OverlayProbeResult{}, fmt.Errorf("overlay media probe: %w", err)
	}
	if info == nil {
		return capoverlay.OverlayProbeResult{}, fmt.Errorf("overlay media probe: nil MediaInfo")
	}
	sha, _, err := digest.SHA256File(path)
	if err != nil {
		return capoverlay.OverlayProbeResult{}, fmt.Errorf("overlay media hash: %w", err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		return capoverlay.OverlayProbeResult{}, fmt.Errorf("overlay media stat: %w", err)
	}
	return capoverlay.OverlayProbeResult{
		Width:        info.Width,
		Height:       info.Height,
		DurationUS:   info.Duration.Microseconds(),
		FPSNum:       info.FPSNum,
		FPSDen:       info.FPSDen,
		AudioStreams: info.AudioStreamCount,
		Codec:        info.VideoCodec,
		PixelFormat:  info.PixelFormat,
		Container:    info.FormatName,
		SizeBytes:    stat.Size(),
		SHA256:       sha,
	}, nil
}

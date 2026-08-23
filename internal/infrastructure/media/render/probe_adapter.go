package render

import (
	"context"
	"fmt"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
)

// Compile-time check that FFProbeSourceDurationProbe satisfies
// SourceDurationProbe.
var _ stockpipeline.SourceDurationProbe = (*FFProbeSourceDurationProbe)(nil)

// FFProbeSourceDurationProbe is the canonical concrete implementation
// of the stockpipeline.SourceDurationProbe port. It uses the shared
// media execution port so the duration measurement stays consistent
// with the rest of the media stack.
type FFProbeSourceDurationProbe struct {
	proc interface {
		Probe(context.Context, string) (*mediaexec.MediaInfo, error)
	}
}

// NewFFProbeSourceDurationProbe constructs a probe adapter around the
// canonical media execution port.
func NewFFProbeSourceDurationProbe(proc interface {
	Probe(context.Context, string) (*mediaexec.MediaInfo, error)
}) *FFProbeSourceDurationProbe {
	return &FFProbeSourceDurationProbe{
		proc: proc,
	}
}

// ProbeDurationSec returns the source duration in seconds using
// ffprobe. Returns a typed error when the file is missing, empty,
// or not a recognizable video container.
func (p *FFProbeSourceDurationProbe) ProbeDurationSec(ctx context.Context, sourcePath string) (float64, error) {
	info, err := p.proc.Probe(ctx, sourcePath)
	if err != nil {
		return 0, fmt.Errorf("ffprobe source duration probe: %w", err)
	}
	if info == nil {
		return 0, fmt.Errorf("ffprobe source duration probe: nil MediaInfo")
	}
	if !info.HasVideo {
		return 0, fmt.Errorf("ffprobe source duration probe: no video stream in %s", sourcePath)
	}
	return info.Duration.Seconds(), nil
}

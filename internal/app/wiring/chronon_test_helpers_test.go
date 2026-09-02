package wiring

import (
	"context"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

type recordingProbe struct {
	caps cliprender.RendererCapabilities
}

func (p recordingProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return p.caps, nil
}

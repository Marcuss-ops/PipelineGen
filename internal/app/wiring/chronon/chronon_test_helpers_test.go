package chronon

import (
	"context"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

type chrononRecordingProbe struct {
	caps cliprender.RendererCapabilities
}

func (p chrononRecordingProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return p.caps, nil
}

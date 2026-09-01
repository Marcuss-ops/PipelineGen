package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MediaSamplerPort is the single selection boundary for media candidates.
// Providers may discover candidates, but only this port may choose a winner.
type MediaSamplerPort interface {
	Sample(ctx context.Context, sceneID, subject string, terms []string, candidates []scriptpkg.SegmentAssetCandidate, allowReuse bool) (string, error)
}

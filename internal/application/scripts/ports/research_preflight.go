package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ResearchPreflight validates source-cache policies before script.generate is
// enqueued. It never performs web search or page fetching.
type ResearchPreflight interface {
	Validate(ctx context.Context, item scriptpkg.GenerationItemV2) error
}

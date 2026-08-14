// Package scriptgeneration — provider_fanout.go owns the per-segment provider
// resolution contract. After entity extraction produces retrieval queries, the
// resolver fans out every enabled visual provider (Artlist, internet images,
// image generation) concurrently through a single shared registry, so no
// provider owns its own orchestration and none can block the others.
package scriptgeneration

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// SegmentProviderResolver fans out a single enriched segment's visual provider
// searches. It receives the segment with entities and retrieval queries already
// resolved and returns the segment with candidate assets merged. Implementations
// must dispatch through the shared provider registry and must not duplicate
// per-provider orchestration (search, rate limiting, retry).
type SegmentProviderResolver interface {
	ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error)
}

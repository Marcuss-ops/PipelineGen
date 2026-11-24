package renderqueue

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// OverlayRenderCacheKey is the identity of one reusable overlay render.
type OverlayRenderCacheKey struct {
	RenderKey   string
	PlanContext string
	Epoch       string
}

// Validate reports whether the key is complete enough to address a cache row.
func (k OverlayRenderCacheKey) Validate() error {
	if len(k.RenderKey) != 64 || !digest.IsCanonicalSHA256(k.RenderKey) {
		return fmt.Errorf("overlay render cache key: render key %q is not a 64-hex digest", k.RenderKey)
	}
	if len(k.PlanContext) != 64 || !digest.IsCanonicalSHA256(k.PlanContext) {
		return fmt.Errorf("overlay render cache key: plan context %q is not a 64-hex digest", k.PlanContext)
	}
	if strings.TrimSpace(k.Epoch) == "" {
		return fmt.Errorf("overlay render cache key: epoch is required")
	}
	return nil
}

// Digest is the canonical cache identity.
func (k OverlayRenderCacheKey) Digest() string {
	return digest.SHA256String(strings.Join([]string{
		"overlay-render-cache/v1", k.RenderKey, k.PlanContext, k.Epoch,
	}, "\x00"))
}

// OverlayRenderCache is the narrow persistence port.
type OverlayRenderCache interface {
	Lookup(ctx context.Context, key OverlayRenderCacheKey) (*RenderArtifact, bool, error)
	Store(ctx context.Context, key OverlayRenderCacheKey, artifact RenderArtifact) error
	Invalidate(ctx context.Context, key OverlayRenderCacheKey, reason string) error
}

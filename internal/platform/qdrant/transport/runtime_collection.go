package transport

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// ProductionCollection is the only Qdrant collection readable and writable
// by runtime code. Candidate, versioned, recovery, synthetic, and test
// collections belong to rebuild or emergency tooling only.
const ProductionCollection = schema.ProductionCollection

// RuntimeAlias is retained as the control-plane alias name for compatibility
// with existing lifecycle metadata. Runtime data access does not resolve to a
// candidate through it; the production collection is fixed.
const RuntimeAlias = schema.CanonicalRuntimeAlias

// ResolveRuntimeCollection resolves the canonical runtime alias and verifies
// that it points to the sole production collection. There is deliberately no
// fallback to a physical generation, recovery collection, or synthetic data.
func (c *Client) ResolveRuntimeCollection(ctx context.Context, alias string) (string, error) {
	if alias == "" {
		alias = RuntimeAlias
	}
	if alias != RuntimeAlias {
		return "", fmt.Errorf("qdrant runtime resolver: alias %q is not the canonical runtime alias %q", alias, RuntimeAlias)
	}
	target, err := c.GetAliasTarget(ctx, RuntimeAlias)
	if err != nil {
		if _, ok := err.(*ErrCollectionNotFound); ok {
			return "", nil
		}
		return "", fmt.Errorf("resolve runtime alias %q: %w", RuntimeAlias, err)
	}
	if target == "" {
		return "", nil
	}
	if target != ProductionCollection {
		return "", fmt.Errorf("qdrant runtime resolver: alias %q points to %q; only production collection %q is allowed", RuntimeAlias, target, ProductionCollection)
	}
	return ProductionCollection, nil
}

// ValidateRuntimeCollection rejects every collection except the production
// collection. Rebuild and emergency code must not call this guard for their
// private candidate/recovery targets.
func ValidateRuntimeCollection(name string) error {
	if name != ProductionCollection {
		return fmt.Errorf("qdrant runtime collection %q is forbidden; only %q is allowed", name, ProductionCollection)
	}
	return nil
}

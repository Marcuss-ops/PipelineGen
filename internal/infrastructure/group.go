// Deprecated: use pkg/concurrent.WithContext / Group instead.
// This file delegates to the canonical implementation in pkg/concurrent/.
package platform

import (
	"context"

	pkgconcurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Group is an errgroup-style helper.
// Deprecated: use pkg/concurrent.Group instead.
type Group = pkgconcurrent.Group

// Deprecated: use pkg/concurrent.WithContext.
func WithContext(parent context.Context) (*Group, context.Context) {
	return pkgconcurrent.WithContext(parent)
}

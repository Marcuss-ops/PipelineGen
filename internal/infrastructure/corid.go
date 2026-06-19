// Package corid provides correlation ID context helpers.
//
// Deprecated: use pkg/corid instead. This file delegates to the canonical
// implementation in pkg/corid/ so call sites can migrate incrementally.
package platform

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// Deprecated: use pkg/corid.WithCorrelationID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return corid.WithCorrelationID(ctx, id)
}

// Deprecated: use pkg/corid.FromContext.
func FromContext(ctx context.Context) string {
	return corid.FromContext(ctx)
}

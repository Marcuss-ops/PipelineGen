// Package destination is a backward-compatibility shim re-exporting
// the canonical destination types from internal/domain/asset.
//
// Wave 4C (June 2026) consolidated the destination contracts into
// internal/domain/asset. This shim exists so that consumers in
// internal/media/storage/ that still import internal/core/destination
// compile without change. New code should import
// internal/domain/asset directly.
package destination

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Resolver is the canonical destination resolver interface.
type Resolver = asset.Resolver

// ResolveRequest is the canonical destination resolution request.
type ResolveRequest = asset.ResolveRequest

// ResolveResult is the canonical destination resolution result.
type ResolveResult = asset.ResolveResult

// Ensure the shim satisfies the Resolver interface.
var _ Resolver = (asset.Resolver)(nil)

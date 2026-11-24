package buildbundle

import assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/assets"

// IOpaqueStartFunc is the opaque type for deferred initialisation closures
// returned by Build*Bundle constructors.
type IOpaqueStartFunc func() error

type AssetDestResolver = assetswiring.DestResolver

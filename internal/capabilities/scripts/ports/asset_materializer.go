// Package ports — asset_materializer.go defines the canonical port
// used by the visual planning processor to materialize the winning
// asset of a layer.
//
// godlike/06 SSOT: the port keeps the processor agnostic of the
// concrete stockpipeline / mediamemory machinery. The composition
// root wires a concrete adapter; if no materializer is available the
// processor continues without materializing.
package ports

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// MaterializedAsset is the result of materializing a candidate.
type MaterializedAsset struct {
	// AssetID is the canonical local asset ID after materialization.
	// If materialization is skipped, it matches the input asset ID.
	AssetID string
	// Provider is the canonical source tag after materialization.
	Provider string
	// DurationMs is the asset duration in milliseconds.
	DurationMs int64
}

// AssetMaterializer is the port used by the visual planning processor
// to ensure the winning asset is materialized (downloaded/uploaded)
// before it is bound to a scene. Implementations are idempotent:
// an already-materialized asset is returned as-is.
type AssetMaterializer interface {
	// Materialize ensures the candidate identified by opt is available
	// as a local media asset. It returns the canonical local asset ID
	// and provider. On failure it returns an error; callers should
	// degrade gracefully (e.g. keep the original asset ID and log a
	// warning) rather than failing the whole pipeline.
	Materialize(ctx context.Context, layer mediamemory.Layer) (MaterializedAsset, error)
}

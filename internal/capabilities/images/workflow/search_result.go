package workflow

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// SearchResult carries the canonical image asset plus the retrieval
// trace needed by HTTP callers to expose cache provenance.
type SearchResult struct {
	Asset             *asset.ImageAsset
	CacheHit          bool
	CacheSource       string
	RetrievalProvider string
}

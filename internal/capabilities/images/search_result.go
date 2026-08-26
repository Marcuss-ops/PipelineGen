package images

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

// SearchResult carries the canonical image asset plus the retrieval
// trace needed by HTTP callers to expose cache provenance.
type SearchResult struct {
	Asset             *detail.ImageAsset
	CacheHit          bool
	CacheSource       string
	RetrievalProvider string
}

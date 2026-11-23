package search

// deps.go owns the typed input contracts of the search module.
//
// Build used to take nine positional arguments and SelectMediaSearchStore
// three. Nine positional arguments cannot be validated in one place and a
// re-ordered pair of assignable ports compiles silently — the same failure
// class the drive module closed in BON-03 step A. The named structs below are
// the module's own vocabulary for what it needs; the composition root fills
// them and reads nothing back.

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// StoreDeps is the input contract of SelectMediaSearchStore. Config and
// Database decide whether the canonical PostgreSQL media store exists at all;
// Log is optional (the resolver falls back to a no-op logger).
type StoreDeps struct {
	Config   *config.Config
	Database *sql.DB
	Log      *zap.Logger
}

// Deps is the input contract of Build: every field is an already-built port or
// registry owned by the caller. Providers must be bootstrapped and frozen;
// MediaRepo/VectorStore are the canonical media read surfaces, never a
// compatibility fallback (MEDIA-SSOT P1-6).
type Deps struct {
	Log         *zap.Logger
	Providers   *providers.Registry
	Embeddings  search.EmbeddingChannelRegistry
	VectorStore search.VectorStorePort
	MediaRepo   search.MediaReadRepository
	Delivery    search.AssetDeliveryService
	Reranker    RerankerClient
	Resolver    search.CanonicalIdentityResolver
	LocalScorer search.LocalScorer
}

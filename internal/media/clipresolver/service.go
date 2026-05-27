package clipresolver

import (
	"context"

	"velox/go-master/internal/media/clipcatalog"
	"velox/go-master/internal/pkg/matchingconfig"
)

// Service provides clip recommendation functionality.
type Service struct {
	repos          map[string]*clipcatalog.Repository
	harvestSvc     ArtlistHarvestService
	embedProvider  EmbeddingProvider
	ontologyScorer OntologyScorer
	matchingConfig *matchingconfig.MatchingConfig
	vectorStore    VectorStoreSearcher
}

// ArtlistHarvestService interface for enqueueing harvest jobs.
type ArtlistHarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (jobID string, err error)
}

// NewService creates a new clip resolver service.
func NewService(
	repos map[string]*clipcatalog.Repository,
	harvestSvc ArtlistHarvestService,
	embedProvider EmbeddingProvider,
	ontologyScorer OntologyScorer,
	matchingConfig *matchingconfig.MatchingConfig,
	vectorStore VectorStoreSearcher,
) *Service {
	return &Service{
		repos:          repos,
		harvestSvc:     harvestSvc,
		embedProvider:  embedProvider,
		ontologyScorer: ontologyScorer,
		matchingConfig: matchingConfig,
		vectorStore:    vectorStore,
	}
}

// SetVectorStore sets the vector store searcher for primary ANN search.
func (s *Service) SetVectorStore(vs VectorStoreSearcher) {
	s.vectorStore = vs
}

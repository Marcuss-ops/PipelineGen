package clipresolver

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/media/clipcatalog"
	matchingconfig "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// Service provides clip recommendation functionality.
type Service struct {
	repos          map[string]*clipcatalog.Repository
	harvestSvc     ArtlistHarvestService
	embedProvider  EmbeddingProvider
	ontologyScorer OntologyScorer
	matchingConfig *matchingconfig.MatchingConfig
	vectorStore    VectorStoreSearcher
	llmDecision    *LLMDecisionService // Optional LLM-powered final evaluation
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
	llmDecision *LLMDecisionService,
) *Service {
	return &Service{
		repos:          repos,
		harvestSvc:     harvestSvc,
		embedProvider:  embedProvider,
		ontologyScorer: ontologyScorer,
		matchingConfig: matchingConfig,
		vectorStore:    vectorStore,
		llmDecision:    llmDecision,
	}
}

// SetLLMDecisionService sets or replaces the LLM decision layer after construction.
func (s *Service) SetLLMDecisionService(llm *LLMDecisionService) {
	s.llmDecision = llm
}

// SetVectorStore sets the vector store searcher for primary ANN search.
func (s *Service) SetVectorStore(vs VectorStoreSearcher) {
	s.vectorStore = vs
}

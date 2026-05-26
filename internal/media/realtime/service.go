package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media/vectorstore"
)

// EmbeddingClient is the interface for calling the embedding server.
type EmbeddingClient interface {
	EmbedText(ctx context.Context, text string) ([]float64, error)
}

// JobService is the interface for enqueueing background generation jobs.
type JobService interface {
	EnqueueMediaGeneration(ctx context.Context, query string, source string) (string, error)
}

// Service handles real-time asset matching using vector search + fallback.
type Service struct {
	vectorSvc  *vectorstore.Service
	embedder   EmbeddingClient
	jobSvc     JobService
	cfg        *config.VectorSearchConfig
	log        *zap.Logger

	// Query embedding cache
	embeddingCache map[string][]float32
}

// NewService creates a new realtime match service.
func NewService(
	vectorSvc *vectorstore.Service,
	embedder EmbeddingClient,
	jobSvc JobService,
	cfg *config.VectorSearchConfig,
	log *zap.Logger,
) *Service {
	return &Service{
		vectorSvc:      vectorSvc,
		embedder:       embedder,
		jobSvc:         jobSvc,
		cfg:            cfg,
		log:            log,
		embeddingCache: make(map[string][]float32),
	}
}

// Match performs a real-time match of a query against the vector index.
func (s *Service) Match(ctx context.Context, req *MatchRequest) (*MatchResponse, error) {
	start := time.Now()

	resp := &MatchResponse{
		OK:     true,
		Status: "no_match",
	}

	if req.Query == "" {
		return resp, fmt.Errorf("empty query")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 3
	}

	minScore := req.MinScore
	if minScore <= 0 {
		minScore = s.cfg.MinInstantScore
	}

	vectorName := s.cfg.TextVectorName
	mode := req.Mode
	if mode == "" {
		mode = "text"
	}
	if mode == "visual" {
		vectorName = s.cfg.VisualVectorName
	}

	// Step 1: Get query embedding (cached if seen before)
	queryVec, err := s.getEmbedding(ctx, req.Query)
	if err != nil {
		s.log.Warn("failed to get query embedding, falling back",
			zap.Error(err))
		resp.Status = "embedding_failed"
		resp.LatencyMs = time.Since(start).Milliseconds()
		return resp, nil
	}

	// Step 2: Qdrant ANN search
	searchResults, err := s.vectorSvc.Search(ctx, vectorstore.SearchRequest{
		QueryVector: queryVec,
		VectorName:  vectorName,
		Limit:       limit,
		MinScore:    minScore,
		Source:      req.Source,
		Category:    req.Category,
		MediaType:   req.MediaType,
	})
	if err != nil {
		// Log but don't fail — fallback may still work
		s.log.Warn("vector search failed", zap.Error(err))
	}

	// Step 3: Process results
	if len(searchResults) > 0 {
		top := searchResults[0]
		resp.Status = "instant_match"
		resp.Asset = &MatchAsset{
			ID:        top.AssetID,
			Score:     top.Score,
			Source:    top.Source,
			Name:      top.Name,
			LocalPath: top.LocalPath,
			DriveLink: top.DriveLink,
			Category:  top.Category,
			MediaType: top.MediaType,
		}

		// If score is very high, return immediately
		if top.Score >= minScore {
			resp.LatencyMs = time.Since(start).Milliseconds()
			return resp, nil
		}
	}

	// Step 4: No high-score match — check for fallback or generation
	resp.Status = "fallback_used"
	latencyMs := time.Since(start).Milliseconds()
	resp.LatencyMs = latencyMs

	// If we have results but below threshold, return best as fallback
	if len(searchResults) > 0 {
		top := searchResults[0]
		resp.FallbackAsset = &MatchAsset{
			ID:        top.AssetID,
			Score:     top.Score,
			Source:    top.Source,
			Name:      top.Name,
			LocalPath: top.LocalPath,
			DriveLink: top.DriveLink,
		}
	}

	// Step 5: Enqueue background generation if enabled
	shouldGen := req.AllowBackgroundGen
	if !shouldGen {
		shouldGen = s.cfg.AllowBackgroundGen
	}

	if shouldGen && s.jobSvc != nil {
		jobID, err := s.jobSvc.EnqueueMediaGeneration(ctx, req.Query, req.Source)
		if err != nil {
			resp.GenerationError = err.Error()
			s.log.Warn("failed to enqueue generation job", zap.Error(err))
		} else {
			resp.GenerationJobID = jobID
			resp.Status = "fallback_generating"
		}
	}

	return resp, nil
}

// getEmbedding returns a cached or fresh query embedding.
func (s *Service) getEmbedding(ctx context.Context, query string) ([]float32, error) {
	// Check cache
	if cached, ok := s.embeddingCache[query]; ok {
		return cached, nil
	}

	if s.embedder == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}

	// Call Python embedding server
	emb64, err := s.embedder.EmbedText(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}

	// Convert float64 → float32 for Qdrant
	emb32 := make([]float32, len(emb64))
	for i, v := range emb64 {
		emb32[i] = float32(v)
	}

	// Cache
	s.embeddingCache[query] = emb32

	return emb32, nil
}

// ClearEmbeddingCache resets the in-memory query embedding cache.
func (s *Service) ClearEmbeddingCache() {
	s.embeddingCache = make(map[string][]float32)
}

// toJSON is a helper for serialisation.
func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

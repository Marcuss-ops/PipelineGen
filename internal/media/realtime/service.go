package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/reranker"
)

// EmbeddingClient is the interface for calling the embedding server.
type EmbeddingClient interface {
	EmbedText(ctx context.Context, text string) ([]float64, error)
	EmbedVisual(ctx context.Context, text string) ([]float64, error)
	EmbedAudio(ctx context.Context, text string) ([]float64, error)
}

// JobService is the interface for enqueueing background generation jobs.
type JobService interface {
	EnqueueMediaGeneration(ctx context.Context, query string, source string) (string, error)
}

// Service handles real-time asset matching using vector search + CrossEncoder rerank + fallback.
// Multi-media ready: works for clips, stock, artlist, images, voiceovers, AI video.
type Service struct {
	vectorSvc  *vectorstore.Service
	embedder   EmbeddingClient
	jobSvc     JobService
	reranker   *reranker.Client
	rerankCfg  config.RerankerConfig
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
	rerankerClient *reranker.Client,
	rerankCfg config.RerankerConfig,
	cfg *config.VectorSearchConfig,
	log *zap.Logger,
) *Service {
	return &Service{
		vectorSvc:      vectorSvc,
		embedder:       embedder,
		jobSvc:         jobSvc,
		reranker:       rerankerClient,
		rerankCfg:      rerankCfg,
		cfg:            cfg,
		log:            log,
		embeddingCache: make(map[string][]float32),
	}
}

// Match performs a real-time match of a query against the vector index.
// Pipeline: Embed → Qdrant → Reranker (optional) → Mixed Scoring → Fallback/Generation.
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
	switch mode {
	case "visual":
		vectorName = s.cfg.VisualVectorName
	case "audio":
		vectorName = s.cfg.AudioVectorName
	}

	// Step 1: Get query embedding (cached if seen before)
	queryVec, err := s.getEmbeddingForVector(ctx, req.Query, mode)
	if err != nil {
		s.log.Warn("failed to get query embedding, falling back",
			zap.String("mode", mode), zap.Error(err))
		resp.Status = "embedding_failed"
		resp.LatencyMs = time.Since(start).Milliseconds()
		return resp, nil
	}

	// Step 2: Qdrant ANN search — fetch top_k candidates for reranker
	topK := s.rerankCfg.TopK
	if topK <= 0 {
		topK = 30
	}
	searchResults, err := s.vectorSvc.Search(ctx, vectorstore.SearchRequest{
		QueryVector: queryVec,
		VectorName:  vectorName,
		Limit:       topK,
		MinScore:    minScore * 0.5, // relaxed threshold for reranker
		Source:      req.Source,
		Category:    req.Category,
		MediaType:   req.MediaType,
	})
	if err != nil {
		s.log.Warn("vector search failed", zap.Error(err))
	}

	// Step 2.5: CrossEncoder Reranking (optional, circuit breaker pattern)
	rerankUsed := false
	if s.reranker != nil && s.reranker.IsEnabled() && len(searchResults) > 1 {
		candidates := make([]reranker.Candidate, len(searchResults))
		for i, r := range searchResults {
			// Build rich candidate text for CrossEncoder precision.
			// Now uses SearchText (rich FTS blob), Tags, Style — not just Name/Category.
			// This enables bge-reranker-v2-m3 to compare query vs rich passage for 100+ languages.
			candidates[i] = reranker.Candidate{
				ID:          r.AssetID,
				Text:        reranker.BuildCandidateText(r.Name, r.SearchText, r.Tags, r.Style, r.Category, r.MediaType),
				QdrantScore: &r.Score,
			}
		}

		if reranked, err := s.reranker.Rerank(ctx, req.Query, candidates); err == nil && len(reranked) > 0 {
			rerankUsed = true

			// Build maps for normalization and mixed scoring
			rerankScores := make(map[string]float64, len(reranked))
			qdrantScores := make(map[string]float64, len(searchResults))
			for _, rr := range reranked {
				rerankScores[rr.ID] = rr.RerankScore
			}
			for _, r := range searchResults {
				qdrantScores[r.AssetID] = r.Score
			}

			// Normalize reranker scores to [0, 1]
			normScores := reranker.NormalizeScores(rerankScores)

			// Apply mixed scoring: Qdrant (bi-encoder) + Reranker (cross-encoder)
			weight := s.rerankCfg.Weight
			finalScores := make(map[string]float64, len(searchResults))
			for _, r := range searchResults {
				qScore := qdrantScores[r.AssetID]
				rScore := normScores[r.AssetID]
				finalScores[r.AssetID] = reranker.MixedScore(qScore, rScore, weight)
			}

			// Sort results by final mixed score descending
			type scored struct {
				r     vectorstore.SearchResult
				score float64
			}
			sorted := make([]scored, 0, len(searchResults))
			for _, r := range searchResults {
				sorted = append(sorted, scored{r: r, score: finalScores[r.AssetID]})
			}
			// Simple bubble sort for small arrays (top 30)
			for i := 0; i < len(sorted)-1; i++ {
				for j := i + 1; j < len(sorted); j++ {
					if sorted[j].score > sorted[i].score {
						sorted[i], sorted[j] = sorted[j], sorted[i]
					}
				}
			}

			reordered := make([]vectorstore.SearchResult, len(sorted))
			for i, sc := range sorted {
				reordered[i] = sc.r
				reordered[i].Score = sc.score
			}
			searchResults = reordered

			s.log.Debug("reranker reordered candidates (mixed scoring)",
				zap.Int("candidates", len(searchResults)),
				zap.String("top_id", searchResults[0].AssetID),
				zap.Float64("top_score", searchResults[0].Score),
				zap.Float64("weight", weight),
			)
		} else {
			s.log.Debug("reranker unavailable, using Qdrant order",
				zap.Int("candidates", len(candidates)),
				zap.Error(err),
			)
		}
	}

	// Step 3: Select best result
	if len(searchResults) > 0 {
		top := searchResults[0]
		resp.Status = "instant_match"
		if rerankUsed {
			resp.Status = "instant_match_reranked"
		}
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

		// If score is high enough, return immediately
		if top.Score >= minScore {
			resp.LatencyMs = time.Since(start).Milliseconds()
			return resp, nil
		}
	}

	// Step 4: No high-score match — provide fallback
	resp.Status = "fallback_used"
	resp.LatencyMs = time.Since(start).Milliseconds()

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

// getEmbeddingForVector returns a cached or fresh query embedding for a specific vector space.
func (s *Service) getEmbeddingForVector(ctx context.Context, query string, mode string) ([]float32, error) {
	cacheKey := mode + ":" + query
	if cached, ok := s.embeddingCache[cacheKey]; ok {
		return cached, nil
	}

	if s.embedder == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}

	var emb64 []float64
	var err error

	switch mode {
	case "visual":
		emb64, err = s.embedder.EmbedVisual(ctx, query)
	case "audio":
		emb64, err = s.embedder.EmbedAudio(ctx, query)
	default:
		emb64, err = s.embedder.EmbedText(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("embedding failed for mode %s: %w", mode, err)
	}

	emb32 := make([]float32, len(emb64))
	for i, v := range emb64 {
		emb32[i] = float32(v)
	}

	s.embeddingCache[cacheKey] = emb32
	return emb32, nil
}

// getEmbedding returns a cached or fresh query embedding.
func (s *Service) getEmbedding(ctx context.Context, query string) ([]float32, error) {
	return s.getEmbeddingForVector(ctx, query, "text")
}

// ClearEmbeddingCache resets the in-memory query embedding cache.
func (s *Service) ClearEmbeddingCache() {
	s.embeddingCache = make(map[string][]float32)
}

// EmbedText computes the embedding vector for the text using the Python server.
func (s *Service) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return s.getEmbedding(ctx, text)
}

// EmbedTextForVector computes the embedding vector for the text using the specified vector space.
func (s *Service) EmbedTextForVector(ctx context.Context, text string, mode string) ([]float32, error) {
	return s.getEmbeddingForVector(ctx, text, mode)
}

// VectorStore returns the underlying vector store service.
func (s *Service) VectorStore() *vectorstore.Service {
	return s.vectorSvc
}

// toJSON is a helper for serialisation.
func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

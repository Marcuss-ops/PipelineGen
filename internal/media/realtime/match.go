package realtime

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/reranker"
)

func (s *Service) Match(ctx context.Context, req *MatchRequest) (*MatchResponse, error) {
	start := time.Now()

	resp := &MatchResponse{
		OK:     true,
		Status: "no_match",
	}

	if strings.TrimSpace(req.Query) == "" {
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

	var queryVec []float32
	var normalizedQuery = req.Query
	var err error

	if mode == "text" || mode == "" {
		emb64, normText, embedErr := s.embedder.EmbedTextWithNormalized(ctx, req.Query)
		if embedErr == nil {
			normalizedQuery = normText
			queryVec = make([]float32, len(emb64))
			for i, v := range emb64 {
				queryVec[i] = float32(v)
			}
			s.cacheMu.Lock()
			s.embeddingCache[mode+":"+req.Query] = queryVec
			s.cacheMu.Unlock()
		} else {
			s.log.Warn("failed to get normalized embedding, falling back", zap.Error(embedErr))
		}
	}

	if queryVec == nil {
		queryVec, err = s.getEmbeddingForVector(ctx, req.Query, mode)
		if err != nil {
			s.log.Warn("failed to get query embedding, falling back",
				zap.String("mode", mode), zap.Error(err))
			resp.Status = "embedding_failed"
			resp.LatencyMs = time.Since(start).Milliseconds()
			return resp, nil
		}
	}

	topK := s.rerankCfg.TopK
	if topK <= 0 {
		topK = 30
	}
	var searchResults []vectorstore.SearchResult
	if mode == "text" || mode == "" {
		searchResults, err = s.vectorSvc.HybridSearch(ctx, vectorstore.HybridSearchRequest{
			QueryText:       normalizedQuery,
			DenseVector:     queryVec,
			DenseVectorName: vectorName,
			Limit:           topK,
			MinScore:        minScore * 0.5,
			Source:          req.Source,
			Category:        req.Category,
			MediaType:       req.MediaType,
		})
	} else {
		searchResults, err = s.vectorSvc.Search(ctx, vectorstore.SearchRequest{
			QueryVector: queryVec,
			VectorName:  vectorName,
			Limit:       topK,
			MinScore:    minScore * 0.5,
			Source:      req.Source,
			Category:    req.Category,
			MediaType:   req.MediaType,
		})
	}
	if err != nil {
		s.log.Warn("vector search failed", zap.Error(err))
	}

	rerankUsed := false
	if s.reranker != nil && s.reranker.IsEnabled() && len(searchResults) > 1 {
		candidates := make([]reranker.Candidate, len(searchResults))
		for i, r := range searchResults {
			candidates[i] = reranker.Candidate{
				ID:          r.AssetID,
				Text:        reranker.BuildCandidateText(r.Name, r.SearchText, r.Tags, r.Style, r.Category, r.MediaType),
				QdrantScore: &r.Score,
			}
		}

		if reranked, err := s.reranker.Rerank(ctx, req.Query, candidates); err == nil && len(reranked) > 0 {
			rerankUsed = true

			rerankScores := make(map[string]float64, len(reranked))
			qdrantScores := make(map[string]float64, len(searchResults))
			for _, rr := range reranked {
				rerankScores[rr.ID] = rr.RerankScore
			}
			for _, r := range searchResults {
				qdrantScores[r.AssetID] = r.Score
			}

			normScores := reranker.NormalizeScores(rerankScores)

			weight := s.rerankCfg.Weight
			finalScores := make(map[string]float64, len(searchResults))
			for _, r := range searchResults {
				qScore := qdrantScores[r.AssetID]
				rScore := normScores[r.AssetID]
				finalScores[r.AssetID] = reranker.MixedScore(qScore, rScore, weight)
			}

			type scored struct {
				r     vectorstore.SearchResult
				score float64
			}
			sorted := make([]scored, 0, len(searchResults))
			for _, r := range searchResults {
				sorted = append(sorted, scored{r: r, score: finalScores[r.AssetID]})
			}
			slices.SortFunc(sorted, func(a, b scored) int {
				return cmp.Compare(b.score, a.score)
			})

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

		if top.Score >= minScore {
			resp.LatencyMs = time.Since(start).Milliseconds()
			return resp, nil
		}
	}

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

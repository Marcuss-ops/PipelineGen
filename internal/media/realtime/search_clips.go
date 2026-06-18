package realtime

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
)

func (s *Service) SearchClips(ctx context.Context, query string, source string, mediaType string, limit int, minScore float64) ([]MatchAsset, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty query")
	}
	if s.embedder == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}
	if s.vectorSvc == nil {
		return nil, fmt.Errorf("vector store not configured")
	}
	if limit <= 0 {
		limit = 3
	}
	if minScore <= 0 {
		minScore = 0.45
	}

	emb64, normalizedQuery, err := s.embedder.EmbedTextWithNormalized(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	queryVec := make([]float32, len(emb64))
	for i, v := range emb64 {
		queryVec[i] = float32(v)
	}

	topK := s.rerankCfg.TopK
	if topK <= 0 {
		topK = 30
	}
	searchResults, err := s.vectorSvc.HybridSearch(ctx, vectorstore.HybridSearchRequest{
		QueryText:            normalizedQuery,
		DenseVector:          queryVec,
		DenseVectorName:      s.cfg.TextVectorName,
		TranscriptVector:     queryVec,
		TranscriptVectorName: s.cfg.TranscriptVectorName,
		Limit:                topK,
		MinScore:             minScore * 0.5,
		Source:               source,
		MediaType:            mediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	if s.reranker != nil && s.reranker.IsEnabled() && len(searchResults) > 1 {
		candidates := make([]reranker.Candidate, len(searchResults))
		for i, r := range searchResults {
			candidates[i] = reranker.Candidate{
				ID:          r.AssetID,
				Text:        reranker.BuildCandidateText(r.Name, r.SearchText, r.Tags, r.Style, r.Category, r.MediaType),
				QdrantScore: &r.Score,
			}
		}

		if reranked, err := s.reranker.Rerank(ctx, query, candidates); err == nil && len(reranked) > 0 {
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
				finalScores[r.AssetID] = reranker.MixedScore(qdrantScores[r.AssetID], normScores[r.AssetID], weight)
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

			reordered := make([]vectorstore.SearchResult, 0, len(sorted))
			for _, sc := range sorted {
				sc.r.Score = sc.score
				reordered = append(reordered, sc.r)
			}
			searchResults = reordered
		} else if err != nil {
			s.log.Debug("reranker unavailable for SearchClips, using Qdrant order",
				zap.Int("candidates", len(candidates)),
				zap.Error(err),
			)
		}
	}

	assets := make([]MatchAsset, 0, len(searchResults))
	for _, r := range searchResults {
		if r.Score < minScore {
			continue
		}
		assets = append(assets, MatchAsset{
			ID:         r.AssetID,
			Score:      r.Score,
			Source:     r.Source,
			Name:       cleanClipName(r.Name),
			LocalPath:  r.LocalPath,
			DriveLink:  r.DriveLink,
			Category:   r.Category,
			MediaType:  r.MediaType,
			YouTubeURL: r.YouTubeURL,
			StartTime:  r.StartTime,
			EndTime:    r.EndTime,
		})
		if len(assets) >= limit {
			break
		}
	}

	return assets, nil
}

func cleanClipName(name string) string {
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "&nbsp;", " ")
	name = strings.ReplaceAll(name, "&gt;", " ")
	name = strings.ReplaceAll(name, "&lt;", " ")
	name = strings.ReplaceAll(name, "&amp;", "&")
	name = strings.ReplaceAll(name, "&quot;", "\"")
	name = strings.ReplaceAll(name, "&#39;", "'")
	for _, prefix := range []string{"https://", "http://", "https", "http", "www.", "www"} {
		name = strings.ReplaceAll(name, prefix, " ")
	}
	noiseTokens := map[string]struct{}{
		"nbsp": {}, "code": {}, "watch": {}, "listen": {}, "subscribe": {},
		"channel": {}, "official": {}, "com": {}, "net": {}, "org": {},
		"video": {}, "videos": {}, "clip": {}, "clips": {},
	}
	fields := strings.Fields(name)
	filtered := make([]string, 0, len(fields))
	for _, f := range fields {
		lower := strings.ToLower(strings.Trim(f, ",.;:!?()[]{}<>\"'"))
		if lower == "" {
			continue
		}
		if _, isNoise := noiseTokens[lower]; isNoise {
			continue
		}
		if strings.Contains(lower, ".com") || strings.Contains(lower, ".net") ||
			strings.Contains(lower, ".org") || strings.Contains(lower, "youtu.be") {
			continue
		}
		filtered = append(filtered, f)
	}
	return strings.TrimSpace(strings.Join(filtered, " "))
}

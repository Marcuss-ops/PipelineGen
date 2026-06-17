package mediacurator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/service/scriptcore"
	"velox/go-master/pkg/metrics"
)

// ── Search (with multi-query expansion) ───────────────────────────────────

// extractKeywords splits a long query into focused single-concept keywords.
// E.g. "funny actors parenting stories" → ["actors", "funny", "parenting"]
func extractKeywords(query string) []string {
	words := strings.Fields(query)
	if len(words) <= 2 {
		return nil // short queries don't need expansion
	}

	// Filter out very common stopwords that don't carry semantic weight
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"in": true, "on": true, "at": true, "to": true, "for": true,
		"of": true, "with": true, "from": true, "by": true, "is": true,
		"are": true, "was": true, "were": true, "be": true, "been": true,
		"has": true, "have": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "can": true, "shall": true,
		"its": true, "it's": true, "that": true, "this": true, "these": true,
		"those": true, "i": true, "you": true, "he": true, "she": true,
		"we": true, "they": true, "me": true, "him": true, "her": true,
		"us": true, "them": true, "my": true, "your": true, "his": true,
		"their": true, "our": true, "not": true, "no": true, "but": true,
		"about": true, "what": true, "who": true, "where": true, "when": true,
		"how": true, "all": true, "each": true, "every": true, "some": true,
		"any": true, "more": true, "most": true, "other": true, "such": true,
		"only": true, "own": true, "same": true, "so": true, "than": true,
		"too": true, "very": true, "just": true, "because": true, "as": true,
		"into": true, "over": true, "between": true, "through": true,
		"during": true, "before": true, "after": true, "above": true,
		"below": true, "up": true, "down": true, "out": true, "off": true,
		"under": true, "again": true, "further": true, "once": true, "here": true,
		"there": true, "then": true, "also": true, "well": true,
	}

	keywords := make([]string, 0, len(words))
	seen := make(map[string]bool)
	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, ".,!?;:'\"()[]"))
		if w == "" || stopwords[w] || seen[w] || len(w) < 3 {
			continue
		}
		seen[w] = true
		keywords = append(keywords, w)
	}

	// Limit to at most 5 keywords to keep search fast
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}

	return keywords
}

// searchClips searches for clips matching the query.
// Primary path: Qdrant hybrid search (requires embedder + vectorstore).
// Fallback path: SQLite LIKE on name, tags, and metadata columns when
// the embedding server is unavailable.
func (s *Service) searchClips(ctx context.Context, query string, source string, mediaType string, limit int, minScore float64) ([]SearchResultInfo, error) {
	start := time.Now()

	// ── Primary: Qdrant hybrid search (se disponibile) ─────────────────────
	if s.vectorSvc != nil && s.embedder != nil {
		infos, err := s.searchClipsQdrant(ctx, query, source, mediaType, limit, minScore)
		if err == nil && len(infos) > 0 {
			metrics.MediaCuratorSearchTotal.WithLabelValues("qdrant").Inc()
			metrics.MediaCuratorSearchDuration.WithLabelValues("qdrant").Observe(time.Since(start).Seconds())
			return infos, nil
		}
		s.log.Warn("Qdrant search returned no results or failed, trying LIKE fallback",
			zap.Error(err), zap.Int("qdrant_results", len(infos)))
	}

	// ── Fallback: SQLite LIKE search ───────────────────────────────────────
	if s.clipsRepo != nil {
		s.log.Warn("falling back to SQLite LIKE search",
			zap.Bool("qdrant", s.vectorSvc != nil),
			zap.Bool("embedder", s.embedder != nil))
		infos, err := s.likeSearchClips(ctx, query, source, limit)
		if err != nil {
			metrics.MediaCuratorSearchTotal.WithLabelValues("error").Inc()
			metrics.MediaCuratorSearchDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
			return nil, err
		}
		metrics.MediaCuratorSearchTotal.WithLabelValues("like").Inc()
		metrics.MediaCuratorSearchDuration.WithLabelValues("like").Observe(time.Since(start).Seconds())
		return infos, nil
	}

	metrics.MediaCuratorSearchTotal.WithLabelValues("error").Inc()
	metrics.MediaCuratorSearchDuration.WithLabelValues("error").Observe(time.Since(start).Seconds())
	return nil, fmt.Errorf("no search backend available: vectorstore=%v embedder=%v clipsRepo=%v",
		s.vectorSvc != nil, s.embedder != nil, s.clipsRepo != nil)
}

// searchClipsQdrant searches via Qdrant hybrid search with multi-query expansion.
func (s *Service) searchClipsQdrant(ctx context.Context, query string, source string, mediaType string, limit int, minScore float64) ([]SearchResultInfo, error) {
	if limit <= 0 {
		limit = 20
	}
	if minScore <= 0 {
		minScore = 0.45
	}

	// Primary: full-query search
	infos, err := s.hybridSearchQuery(ctx, query, source, mediaType, limit, minScore)
	if err != nil {
		s.log.Warn("primary hybrid search failed, trying keyword expansion", zap.Error(err))
		infos = nil
	}

	// Expansion: if primary returned too few, try keyword-by-keyword
	if len(infos) < limit/2 {
		keywords := extractKeywords(query)
		if len(keywords) > 0 {
			s.log.Info("expanding search with individual keywords",
				zap.Strings("keywords", keywords),
				zap.Int("initial_results", len(infos)))

			merged := make(map[string]SearchResultInfo)
			for _, info := range infos {
				merged[info.ClipID] = info
			}

			for _, kw := range keywords {
				kwInfos, kwErr := s.hybridSearchQuery(ctx, kw, source, mediaType, limit, minScore*0.85)
				if kwErr != nil {
					s.log.Debug("keyword search failed", zap.String("keyword", kw), zap.Error(kwErr))
					continue
				}
				for _, info := range kwInfos {
					existing, ok := merged[info.ClipID]
					if !ok || info.Score > existing.Score {
						merged[info.ClipID] = info
					}
				}
			}

			infos = make([]SearchResultInfo, 0, len(merged))
			for _, info := range merged {
				infos = append(infos, info)
			}
			sortInfosByScore(infos)

			if len(infos) > limit {
				infos = infos[:limit]
			}

			s.log.Info("keyword expansion complete",
				zap.Int("after_expansion", len(infos)))
		}
	}

	return infos, nil
}

// hybridSearchQuery performs a single hybrid search query and converts results.
func (s *Service) hybridSearchQuery(ctx context.Context, query string, source string, mediaType string, limit int, minScore float64) ([]SearchResultInfo, error) {
	emb64, normalizedQuery, err := s.embedder.EmbedTextWithNormalized(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query %q: %w", query, err)
	}

	queryVec := make([]float32, len(emb64))
	for i, v := range emb64 {
		queryVec[i] = float32(v)
	}

	results, err := s.vectorSvc.HybridSearch(ctx, vectorstore.HybridSearchRequest{
		QueryText:            normalizedQuery,
		DenseVector:          queryVec,
		DenseVectorName:      "text",
		TranscriptVector:     queryVec,
		TranscriptVectorName: "transcript",
		Limit:                limit * 2, // fetch extra for filtering
		MinScore:             minScore * 0.5,
		Source:               source,
		MediaType:            mediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	infos := make([]SearchResultInfo, 0, len(results))
	for _, r := range results {
		if r.Score < minScore {
			continue
		}
		infos = append(infos, SearchResultInfo{
			ClipID:    r.AssetID,
			Name:      r.Name,
			Score:     r.Score,
			Source:    r.Source,
			DriveLink: r.DriveLink,
		})
		if len(infos) >= limit {
			break
		}
	}
	return infos, nil
}

// sortInfosByScore sorts search results descending by score.
func sortInfosByScore(infos []SearchResultInfo) {
	sort.Slice(infos, func(i, j int) bool { return infos[i].Score > infos[j].Score })
}

// ── SQLite LIKE fallback (embedding server offline) ─────────────────────

// likeSearchClips searches clips via SQLite LIKE on name, tags, and metadata
// columns (summary, topics, speakers, hook, etc.). Used as fallback when the
// embedding server is unavailable.
func (s *Service) likeSearchClips(ctx context.Context, query string, source string, limit int) ([]SearchResultInfo, error) {
	keywords := strings.Fields(query)
	if len(keywords) == 0 {
		return nil, fmt.Errorf("empty query")
	}

	assets, err := s.clipsRepo.SearchClipsByKeywords(ctx, source, keywords, limit)
	if err != nil {
		return nil, fmt.Errorf("LIKE search: %w", err)
	}

	infos := make([]SearchResultInfo, 0, len(assets))
	for _, a := range assets {
		score := 0.5 // base score for LIKE matches (no semantic similarity)
		driveLink := a.DriveLink
		if driveLink == "" {
			driveLink = a.GetMetadataString("drive_link")
		}
		infos = append(infos, SearchResultInfo{
			ClipID:    a.ID,
			Name:      a.Name,
			Score:     score,
			Source:    a.Source,
			DriveLink: driveLink,
		})
	}

	if len(infos) > limit {
		infos = infos[:limit]
	}

	s.log.Info("LIKE search results",
		zap.Int("found", len(infos)),
		zap.Int("limit", limit))

	return infos, nil
}

// filterClipsByIDs returns only the clips whose IDs appear in OrderedClips, preserving
// the order from OrderedClips.
func filterClipsByIDs(clips []scriptcore.ClipEvidence, ordered []scriptcore.OrderedClip) []scriptcore.ClipEvidence {
	clipMap := make(map[string]scriptcore.ClipEvidence, len(clips))
	for _, c := range clips {
		clipMap[c.ClipID] = c
	}
	filtered := make([]scriptcore.ClipEvidence, 0, len(ordered))
	for _, oc := range ordered {
		if c, ok := clipMap[oc.ClipID]; ok {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

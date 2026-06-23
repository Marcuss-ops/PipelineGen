package search

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// Service orchestrates search operations through narrow ports.
type Service struct {
	providers SearchProviderRegistry
	vector    VectorSearchPort
	catalog   LocalCatalogPort
	clips     LocalClipPort
	cfg       ConfigPort
	log       Logger
}

// NewService creates a SearchService.
func NewService(
	providers SearchProviderRegistry,
	vector VectorSearchPort,
	catalog LocalCatalogPort,
	clips LocalClipPort,
	cfg ConfigPort,
	log Logger,
) *Service {
	return &Service{
		providers: providers,
		vector:    vector,
		catalog:   catalog,
		clips:     clips,
		cfg:       cfg,
		log:       log,
	}
}

// ── Cross-provider Search ─────────────────────────────────────────────

// Search fans out to all registered SearchProviders and local catalog/clips.
func (s *Service) Search(ctx context.Context, req SearchRequest) (map[string]any, error) {
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	results := map[string]any{}

	// Fan out to every registered SearchProvider.
	if s.providers != nil {
		for _, p := range s.providers.SearchProviders() {
			if !typeAllowed(p.Capabilities(), req.MediaType) {
			s.log.Debug("provider excluded by type filter",
				"provider", p.Name(),
				"requested_type", req.MediaType)
				continue
			}
			out, err := p.Search(ctx, req)
			source := p.Name()
			if err != nil {
				s.log.Warn("provider search failed", "provider", source, "error", err)
				results[source] = map[string]any{
					"count":   0,
					"results": []any{},
					"error":   err.Error(),
				}
				continue
			}
			results[source] = map[string]any{
				"count":   len(out.Candidates),
				"results": out.Candidates,
				"source":  source,
			}
		}
	}

	// Local catalog.
	if s.catalog != nil {
		catalogResults, err := s.catalog.SearchAll(ctx, req.Query)
		if err != nil {
			s.log.Warn("catalog search failed", "error", err)
		} else {
			results["catalog"] = map[string]any{
				"count":   len(catalogResults),
				"results": catalogResults,
			}
		}
	}

	// Local clips.
	if s.clips != nil && (req.MediaType == "" || req.MediaType == "video" || req.MediaType == "all") {
		localClips, err := s.clips.SearchClips(ctx, "all", req.Query)
		if err != nil {
			s.log.Warn("local clips search failed", "error", err)
		} else {
			results["local"] = map[string]any{
				"count":   len(localClips),
				"results": localClips,
			}
		}
	}

	return map[string]any{
		"query":   req.Query,
		"type":    req.MediaType,
		"results": results,
	}, nil
}

// ── Semantic Search ───────────────────────────────────────────────────

// SemanticSearch runs a Qdrant vector search (ANN or hybrid).
func (s *Service) SemanticSearch(ctx context.Context, req SemanticSearchRequest) (*SemanticSearchResult, error) {
	if s.vector == nil {
		return nil, fmt.Errorf("vector search not configured")
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	vectorName := resolveVectorName(req.VectorName, s.cfg)
	if vectorName == "" {
		return nil, fmt.Errorf("invalid vector name: %q", req.VectorName)
	}

	minScore := req.MinScore
	if minScore <= 0 && s.cfg != nil {
		minScore = s.cfg.VectorConfig().MinInstantScore
	}

	mode := "ann"
	if req.Mode == "hybrid" {
		mode = "hybrid"
	}

	s.log.Info("semantic search",
		"query", req.Query,
		"vector", vectorName,
		"mode", mode,
		"min_score", minScore)

	queryVector, err := s.vector.EmbedTextForVector(ctx, req.Query, vectorName)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	var results []qdrant.SearchResult
	if mode == "hybrid" {
		vc := s.cfg.VectorConfig()
		results, err = s.vector.VectorStore().HybridSearch(ctx, qdrant.HybridSearchRequest{
			QueryText:        req.Query,
			DenseVector:      queryVector,
			DenseVectorName:  vectorName,
			SparseVectorName: vc.TranscriptVectorName,
			Limit:            req.Limit,
			MinScore:         minScore,
			Source:           req.Source,
			MediaType:        req.MediaType,
		})
	} else {
		results, err = s.vector.VectorStore().Search(ctx, qdrant.SearchRequest{
			QueryVector: queryVector,
			VectorName:  vectorName,
			Limit:       req.Limit,
			MinScore:    minScore,
			Source:      req.Source,
			MediaType:   req.MediaType,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}

	// Add reason to each result.
	for i := range results {
		results[i].Reason = buildSearchReason(results[i], req.Query)
	}

	return &SemanticSearchResult{
		Query:    req.Query,
		Vector:   req.VectorName,
		Mode:     mode,
		MinScore: minScore,
		Count:    len(results),
		Results:  results,
	}, nil
}

// ── Recommend ─────────────────────────────────────────────────────────

// Recommend splits script text into scenes and recommends clips per scene.
func (s *Service) Recommend(ctx context.Context, req RecommendRequest) (*RecommendResult, error) {
	if s.vector == nil {
		return nil, fmt.Errorf("vector search not configured")
	}

	req.ScriptText = strings.TrimSpace(req.ScriptText)
	if req.ScriptText == "" {
		return nil, fmt.Errorf("script_text is required")
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	vc := s.cfg.VectorConfig()
	minScore := req.MinScore
	if minScore <= 0 {
		minScore = vc.MinInstantScore
	}
	if minScore <= 0 {
		minScore = 0.5
	}

	scenes := splitScriptIntoScenes(req.ScriptText)
	s.log.Info("recommend: splitting script", "scenes", len(scenes))

	resp := &RecommendResult{
		ScriptPreview: truncate(req.ScriptText, 100),
		SceneCount:    len(scenes),
		Scenes:        make([]RecommendSceneResult, 0, len(scenes)),
		Language:      req.Language,
	}

	totalClips := 0
	used := make(map[string]bool)

	for i, sceneText := range scenes {
		sceneText = strings.TrimSpace(sceneText)
		if sceneText == "" {
			continue
		}
		queryText := cleanQueryText(sceneText)
		if len(queryText) > 300 {
			queryText = queryText[:300]
		}

		queryVector, err := s.vector.EmbedTextForVector(ctx, queryText, "text")
		if err != nil {
			s.log.Warn("recommend: embed failed", "scene", i, "error", err)
			continue
		}

		results, err := s.vector.VectorStore().HybridSearch(ctx, qdrant.HybridSearchRequest{
			QueryText:         cleanQueryText(queryText),
			DenseVector:       queryVector,
			DenseVectorName:   vc.TextVectorName,
			TranscriptVector:  queryVector,
			TranscriptVectorName: vc.TranscriptVectorName,
			Limit:             req.TopK * 2,
			MinScore:          minScore,
			Source:            req.Source,
			MediaType:         req.MediaType,
			Language:          req.Language,
		})
		if err != nil {
			s.log.Warn("recommend: search failed", "scene", i, "error", err)
			continue
		}

		sceneResult := RecommendSceneResult{
			Scene:      truncate(sceneText, 120),
			SceneIndex: i,
			Query:      queryText,
		}
		for _, r := range results {
			if used[r.AssetID] {
				continue
			}
			item := RecommendClipItem{
				AssetID:   r.AssetID,
				Title:     r.Name,
				Score:     r.Score,
				Source:    r.Source,
				MediaType: r.MediaType,
				DriveLink: r.DriveLink,
				Tags:      r.Tags,
				Reason:    buildSearchReason(r, queryText),
			}
			sceneResult.Recommendations = append(sceneResult.Recommendations, item)
			used[r.AssetID] = true
			if len(sceneResult.Recommendations) >= req.TopK {
				break
			}
		}
		resp.Scenes = append(resp.Scenes, sceneResult)
		totalClips += len(sceneResult.Recommendations)
	}
	resp.TotalClips = totalClips

	s.log.Info("recommend: completed", "scenes", len(resp.Scenes), "clips", totalClips)
	return resp, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func resolveVectorName(name string, cfg ConfigPort) string {
	if cfg == nil {
		return ""
	}
	vc := cfg.VectorConfig()
	switch strings.ToLower(name) {
	case "text":
		return vc.TextVectorName
	case "visual":
		return vc.VisualVectorName
	case "audio":
		return vc.AudioVectorName
	}
	return ""
}

func typeAllowed(caps []string, reqType string) bool {
	switch reqType {
	case "", "all", "video":
		return true
	case "audio":
		for _, c := range caps {
			if c == "music" {
				return true
			}
		}
	case "image":
		for _, c := range caps {
			if c == "image" {
				return true
			}
		}
	}
	return false
}

func splitScriptIntoScenes(script string) []string {
	raw := regexp.MustCompile(`\n\s*\n`).Split(script, -1)
	var scenes []string
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(part) < 20 && len(scenes) > 0 {
			scenes[len(scenes)-1] = scenes[len(scenes)-1] + " " + part
		} else {
			scenes = append(scenes, part)
		}
	}
	if len(scenes) <= 1 {
		numbered := regexp.MustCompile(`(?m)^(?:(?:\d+[.)]\s*)|(?:Blocco\s+\d+)|(?:Scene\s+\d+)|(?:##\s*))`).Split(script, -1)
		if len(numbered) > 1 {
			scenes = nil
			for _, part := range numbered {
				part = strings.TrimSpace(part)
				if part != "" && len(part) > 15 {
					scenes = append(scenes, part)
				}
			}
		}
	}
	if len(scenes) <= 1 {
		sentences := regexp.MustCompile(`[.!?]\s+`).Split(script, -1)
		var current strings.Builder
		scenes = nil
		for _, s := range sentences {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if current.Len() > 0 && current.Len()+len(s) > 200 {
				scenes = append(scenes, current.String())
				current.Reset()
			}
			if current.Len() > 0 {
				current.WriteString(" ")
			}
			current.WriteString(s)
		}
		if current.Len() > 0 {
			scenes = append(scenes, current.String())
		}
	}
	if len(scenes) == 0 {
		scenes = []string{script}
	}
	return scenes
}

func cleanQueryText(text string) string {
	text = regexp.MustCompile(`#{1,6}\s+`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`[^\p{L}\p{N}\s.,!?;:'"\-]`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func buildSearchReason(r qdrant.SearchResult, query string) string {
	parts := []string{}
	if r.Score >= 0.85 {
		parts = append(parts, "very high semantic similarity")
	} else if r.Score >= 0.75 {
		parts = append(parts, "high semantic similarity")
	} else if r.Score >= 0.6 {
		parts = append(parts, "good thematic match")
	} else {
		parts = append(parts, "partial semantic match")
	}
	if r.Source != "" {
		parts = append(parts, "source: "+r.Source)
	}
	if r.Language != "" {
		parts = append(parts, "lang: "+r.Language)
	}
	if len(r.Tags) > 0 {
		tags := r.Tags
		if len(tags) > 3 {
			tags = tags[:3]
		}
		parts = append(parts, "tags: "+strings.Join(tags, ", "))
	}
	queryWords := strings.Fields(strings.ToLower(query))
	nameLower := strings.ToLower(r.Name)
	matchCount := 0
	for _, w := range queryWords {
		if len(w) > 2 && strings.Contains(nameLower, w) {
			matchCount++
		}
	}
	if matchCount > 0 {
		parts = append(parts, fmt.Sprintf("name matches %d query terms", matchCount))
	}
	return strings.Join(parts, " | ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

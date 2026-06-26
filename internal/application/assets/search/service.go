package search

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// errSemanticSearchUnavailable is returned when semantic or recommendation
// flows are invoked without a configured vector backend.
var errSemanticSearchUnavailable = errors.New("vector search not configured")

// Service orchestrates search operations through narrow ports. When a
// VectorSearchPort is wired, SemanticSearch and Recommend use it.
// Cross-provider search + local catalog + local clips remain the
// canonical fallback.
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
	if log == nil {
		log = noopLogger{}
	}
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

// SemanticSearch performs a vector search when a VectorSearchPort is wired.
// Returns errSemanticSearchUnavailable when no vector backend is configured.
func (s *Service) SemanticSearch(ctx context.Context, req SemanticSearchRequest) (*SemanticSearchResult, error) {
	if s.vector == nil {
		return nil, errSemanticSearchUnavailable
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	vectorName := strings.TrimSpace(req.VectorName)
	if vectorName == "" {
		vectorName = "text"
	}
	resolvedVectorName := resolveVectorName(vectorName, s.cfg)
	if resolvedVectorName == "" {
		return nil, fmt.Errorf("unknown vector name: %s", vectorName)
	}

	store := s.vector.VectorStore()
	if store == nil {
		return nil, errSemanticSearchUnavailable
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	minScore := req.MinScore
	if minScore <= 0 && s.cfg != nil {
		minScore = s.cfg.VectorConfig().MinInstantScore
	}

	queryVector, err := s.vector.EmbedTextForVector(ctx, query, vectorName)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "ann"
	}

	var results []VectorSearchResult
	switch mode {
	case "ann":
		results, err = store.Search(ctx, VectorSearchRequest{
			QueryVector: queryVector,
			VectorName:  resolvedVectorName,
			Limit:       limit,
			MinScore:    minScore,
			Source:      req.Source,
			MediaType:   req.MediaType,
		})
	case "hybrid":
		transcriptVectorName := "transcript"
		if s.cfg != nil {
			if cfgTranscript := s.cfg.VectorConfig().TranscriptVectorName; cfgTranscript != "" {
				transcriptVectorName = cfgTranscript
			}
		}
		results, err = store.HybridSearch(ctx, HybridSearchRequest{
			QueryText:            query,
			DenseVector:          queryVector,
			DenseVectorName:      resolvedVectorName,
			TranscriptVectorName: transcriptVectorName,
			SparseVectorName:     "bm25_text",
			Limit:                limit,
			MinScore:             minScore,
			Source:               req.Source,
			MediaType:            req.MediaType,
		})
	default:
		return nil, fmt.Errorf("unsupported mode: %s", mode)
	}
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}

	annotated := make([]VectorSearchResult, len(results))
	for i, result := range results {
		if strings.TrimSpace(result.Reason) == "" {
			result.Reason = buildSearchReason(result, query)
		}
		annotated[i] = result
	}

	return &SemanticSearchResult{
		Query:    query,
		Vector:   vectorName,
		Mode:     mode,
		MinScore: minScore,
		Count:    len(annotated),
		Results:  annotated,
	}, nil
}

// ── Recommend ─────────────────────────────────────────────────────────

// Recommend returns scene-based clip recommendations when a
// VectorSearchPort is wired. Returns errSemanticSearchUnavailable when
// no vector backend is configured.
func (s *Service) Recommend(ctx context.Context, req RecommendRequest) (*RecommendResult, error) {
	if s.vector == nil {
		return nil, errSemanticSearchUnavailable
	}

	scriptText := strings.TrimSpace(req.ScriptText)
	if scriptText == "" {
		return nil, fmt.Errorf("script_text is required")
	}

	store := s.vector.VectorStore()
	if store == nil {
		return nil, errSemanticSearchUnavailable
	}

	vectorName := resolveVectorName("text", s.cfg)
	if vectorName == "" {
		return nil, fmt.Errorf("unknown vector name: text")
	}
	transcriptVectorName := "transcript"
	minScore := req.MinScore
	if s.cfg != nil {
		cfg := s.cfg.VectorConfig()
		if cfg.TranscriptVectorName != "" {
			transcriptVectorName = cfg.TranscriptVectorName
		}
		if minScore <= 0 {
			minScore = cfg.MinInstantScore
		}
	}
	if minScore <= 0 {
		minScore = 0.5
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 5
	}

	scenes := splitScriptIntoScenes(scriptText)
	sceneResults := make([]RecommendSceneResult, 0, len(scenes))
	seenAssetIDs := make(map[string]struct{})
	totalClips := 0

	for i, scene := range scenes {
		query := cleanQueryText(scene)
		if query == "" {
			continue
		}

		queryVector, err := s.vector.EmbedTextForVector(ctx, query, "text")
		if err != nil {
			s.log.Warn("recommend embed failed", "scene_index", i, "error", err)
			continue
		}

		vsResults, err := store.HybridSearch(ctx, HybridSearchRequest{
			QueryText:            query,
			DenseVector:          queryVector,
			DenseVectorName:      vectorName,
			TranscriptVectorName: transcriptVectorName,
			SparseVectorName:     "bm25_text",
			Limit:                topK * 2,
			MinScore:             minScore,
			Source:               req.Source,
			MediaType:            req.MediaType,
			Language:             req.Language,
		})
		if err != nil {
			s.log.Warn("recommend scene search failed", "scene_index", i, "error", err)
			continue
		}

		sceneResult := RecommendSceneResult{
			Scene:      truncate(scene, 120),
			SceneIndex: i,
			Query:      query,
		}
		for _, result := range vsResults {
			if result.AssetID == "" || result.Score < minScore {
				continue
			}
			if _, seen := seenAssetIDs[result.AssetID]; seen {
				continue
			}
			if strings.TrimSpace(result.Reason) == "" {
				result.Reason = buildSearchReason(result, query)
			}
			sceneResult.Recommendations = append(sceneResult.Recommendations, RecommendClipItem{
				AssetID:   result.AssetID,
				Title:     result.Name,
				Score:     result.Score,
				Source:    result.Source,
				MediaType: result.MediaType,
				DriveLink: result.DriveLink,
				Tags:      result.Tags,
				Reason:    result.Reason,
			})
			seenAssetIDs[result.AssetID] = struct{}{}
			totalClips++
			if len(sceneResult.Recommendations) >= topK {
				break
			}
		}
		sceneResults = append(sceneResults, sceneResult)
	}

	return &RecommendResult{
		ScriptPreview: truncate(scriptText, 200),
		SceneCount:    len(scenes),
		Scenes:        sceneResults,
		TotalClips:    totalClips,
		Language:      req.Language,
	}, nil
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

func buildSearchReason(r VectorSearchResult, query string) string {
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

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}

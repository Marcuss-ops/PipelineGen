package search

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// errSemanticSearchUnavailable is the sentinel error for callsites that
// reach the semantic-search path when no vector store is wired.
// QDRANT-005 Fase 1 (June 2026): renamed from errSemanticSearchRemoved;
// Qdrant was reintroduced via QDRANT-001..004. The error is now
// conditional — returned only when no vector store backend is configured.
var errSemanticSearchUnavailable = errors.New("semantic search unavailable — no vector store backend configured")

// Service orchestrates search operations through narrow ports.
// QDRANT-005 Fase 1 (June 2026): vector arg restored. When a VectorStorePort
// is wired, SemanticSearch + Recommend use it; otherwise they return
// errSemanticSearchUnavailable. Cross-provider + local catalog + local
// clips remain the canonical fallback.
type Service struct {
	providers SearchProviderRegistry
	catalog   LocalCatalogPort
	clips     LocalClipPort
	cfg       ConfigPort
	log       Logger
	vector    VectorStorePort
}

// NewService creates a SearchService.
// QDRANT-005 Fase 1 (June 2026): vector arg restored.
func NewService(
	providers SearchProviderRegistry,
	catalog LocalCatalogPort,
	clips LocalClipPort,
	cfg ConfigPort,
	log Logger,
	vector VectorStorePort,
) *Service {
	return &Service{
		providers: providers,
		catalog:   catalog,
		clips:     clips,
		cfg:       cfg,
		log:       log,
		vector:    vector,
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

// SemanticSearch performs a vector search when a VectorStorePort is wired.
// QDRANT-005 Fase 1 (June 2026): restored — delegates to the real vector
// store backend if available; returns errSemanticSearchUnavailable otherwise.
func (s *Service) SemanticSearch(ctx context.Context, req SemanticSearchRequest) (*SemanticSearchResult, error) {
	if s.vector == nil {
		return nil, errSemanticSearchUnavailable
	}
	vsReq := VectorSearchRequest{
		QueryVector: nil, // embedding will be resolved by the caller or the vector store
		VectorName:  req.VectorName,
		Limit:       req.Limit,
		MinScore:    req.MinScore,
		Source:      req.Source,
		MediaType:   req.MediaType,
	}
	results, err := s.vector.Search(ctx, vsReq)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}
	return &SemanticSearchResult{
		Query:    req.Query,
		Vector:   req.VectorName,
		Mode:     req.Mode,
		MinScore: req.MinScore,
		Count:    len(results),
		Results:  results,
	}, nil
}

// ── Recommend ─────────────────────────────────────────────────────────

// Recommend returns scene-based clip recommendations when a VectorStorePort
// is wired. QDRANT-005 Fase 1 (June 2026): restored — returns
// errSemanticSearchUnavailable when no vector store is configured.
func (s *Service) Recommend(ctx context.Context, req RecommendRequest) (*RecommendResult, error) {
	if s.vector == nil {
		return nil, errSemanticSearchUnavailable
	}
	// Delegate to semantic search per scene for now; full recommendation
	// pipeline restored in QDRANT-005 Fase 2 (reconciliation).
	results := make([]RecommendSceneResult, 0)
	scenes := splitScriptIntoScenes(req.ScriptText)
	for i, scene := range scenes {
		sr := RecommendSceneResult{Scene: truncate(scene, 120), SceneIndex: i, Query: cleanQueryText(scene)}
		vsReq := VectorSearchRequest{
			VectorName: "text",
			Limit:      req.TopK,
			MinScore:   req.MinScore,
			Source:     req.Source,
			MediaType:  req.MediaType,
		}
		vsResults, err := s.vector.Search(ctx, vsReq)
		if err != nil {
			s.log.Warn("recommend scene search failed", "scene", i, "error", err)
			continue
		}
		for _, r := range vsResults {
			sr.Recommendations = append(sr.Recommendations, RecommendClipItem{
				AssetID:   r.AssetID,
				Title:     r.Name,
				Score:     r.Score,
				Source:    r.Source,
				MediaType: r.MediaType,
				DriveLink: r.DriveLink,
				Tags:      r.Tags,
				Reason:    buildSearchReason(r, req.ScriptText),
			})
		}
		results = append(results, sr)
	}
	return &RecommendResult{
		ScriptPreview: truncate(req.ScriptText, 200),
		SceneCount:    len(scenes),
		Scenes:        results,
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

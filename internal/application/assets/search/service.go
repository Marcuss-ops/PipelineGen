package search

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// errSemanticSearchRemoved is the sentinel error for callsites that
// previously reached for the deleted Qdrant vector-search backend
// (PG-034, June 2026). Surfacing the error keeps existing call paths
// loud rather than silently producing empty result sets.
var errSemanticSearchRemoved = errors.New("semantic search backend removed (PG-034)")

// Service orchestrates search operations through narrow ports.
// PG-034 (June 2026): vector field removed — Qdrant capability deleted.
// Cross-provider search + local catalog + local clips remain canonical.
type Service struct {
	providers SearchProviderRegistry
	catalog   LocalCatalogPort
	clips     LocalClipPort
	cfg       ConfigPort
	log       Logger
}

// NewService creates a SearchService.
// PG-034 (June 2026): vector arg removed — Qdrant capability deleted.
func NewService(
	providers SearchProviderRegistry,
	catalog LocalCatalogPort,
	clips LocalClipPort,
	cfg ConfigPort,
	log Logger,
) *Service {
	return &Service{
		providers: providers,
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

// SemanticSearch was removed in PG-034 (June 2026) — the Qdrant
// vector-search backend was deleted. Callers that previously reached
// for semantic-search should fall back to cross-provider Search on
// local catalog/clips. The SemanticSearchRequest / Result types are
// preserved in ports.go for the rare case a future vector-store
// backend is reintroduced.
//
// The method now returns an errSemanticSearchRemoved so callers that
// still attempt to invoke it get a loud failure instead of an empty
// result set.
func (s *Service) SemanticSearch(ctx context.Context, req SemanticSearchRequest) (*SemanticSearchResult, error) {
	_ = ctx
	_ = req
	return nil, errSemanticSearchRemoved
}

// ── Recommend ─────────────────────────────────────────────────────────

// Recommend was removed in PG-034 (June 2026) — the Qdrant
// vector-search backend was deleted. Callers that previously reached
// for scene-based clip recommendations should fall back to the
// cross-provider Search endpoint on local catalog/clips. Returns
// errSemanticSearchRemoved so callers get a loud failure.
func (s *Service) Recommend(ctx context.Context, req RecommendRequest) (*RecommendResult, error) {
	_ = ctx
	_ = req
	return nil, errSemanticSearchRemoved
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

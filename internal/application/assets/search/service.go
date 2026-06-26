// Package search provides application-layer use cases for media asset search:
// cross-provider search across registered providers, local catalog, and local clips.
//
// Semantic search (vector/hybrid) has been consolidated into
// internal/application/mediasearch. This package handles only provider-based
// and local search.
package search

import (
	"context"
	"fmt"
	"strings"
)

// Service orchestrates search operations through narrow ports.
// Cross-provider + local catalog + local clips only.
type Service struct {
	providers SearchProviderRegistry
	catalog   LocalCatalogPort
	clips     LocalClipPort
	cfg       ConfigPort
	log       Logger
}

// NewService creates a SearchService.
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
func (s *Service) Search(ctx context.Context, req SearchRequest) (*CrossSearchResponse, error) {
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	results := map[string]ProviderResult{}

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
				results[source] = ProviderResult{
					Count:   0,
					Results: []SearchCandidate{},
					Error:   err.Error(),
				}
				continue
			}
			results[source] = ProviderResult{
				Count:   len(out.Candidates),
				Results: out.Candidates,
				Source:  source,
			}
		}
	}

	// Local catalog.
	if s.catalog != nil {
		catalogResults, err := s.catalog.SearchAll(ctx, req.Query)
		if err != nil {
			s.log.Warn("catalog search failed", "error", err)
		} else {
			results["catalog"] = ProviderResult{
				Count:   len(catalogResults),
				Results: toSearchCandidates(catalogResults),
			}
		}
	}

	// Local clips.
	if s.clips != nil && (req.MediaType == "" || req.MediaType == "video" || req.MediaType == "all") {
		localClips, err := s.clips.SearchClips(ctx, "all", req.Query)
		if err != nil {
			s.log.Warn("local clips search failed", "error", err)
		} else {
			results["local"] = ProviderResult{
				Count:   len(localClips),
				Results: toSearchCandidatesFromClips(localClips),
			}
		}
	}

	return &CrossSearchResponse{
		Query:   req.Query,
		Type:    req.MediaType,
		Results: results,
	}, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

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

func toSearchCandidates(results []CatalogSearchResult) []SearchCandidate {
	out := make([]SearchCandidate, len(results))
	for i, r := range results {
		out[i] = SearchCandidate{
			SourceRef: r.ID,
			Title:     r.Name,
			Score:     r.Score,
		}
	}
	return out
}

func toSearchCandidatesFromClips(results []LocalClipResult) []SearchCandidate {
	out := make([]SearchCandidate, len(results))
	for i, r := range results {
		out[i] = SearchCandidate{
			SourceRef: r.ID,
			Title:     r.Name,
		}
	}
	return out
}

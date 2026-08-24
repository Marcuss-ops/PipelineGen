// Package webresearch — multi_provider.go provides the MultiWebSearcher,
// a provider orchestrator that merges results from multiple
// WebSearchProviders. It handles provider iteration, error recovery,
// and cross-provider deduplication — but NOT subject-aware filtering.
//
// Subject validation is the responsibility of the caller (the
// ResearchSearchCoordinator), which sits above this component in the
// architecture:
//
//	IdentityResolver → QueryPlanner → MultiWebSearcher → SubjectFilter → Fetcher
//
// New adapter (August 2026): registered in package_hotspots.json under
// the infrastructure adapter migration owner for multi-provider research
// fallback.
package webresearch

import (
	"context"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"go.uber.org/zap"
)

// MultiWebSearcher orchestrates multiple WebSearchProviders. It iterates
// providers in registered order, merging and deduplicating results.
// Errors from individual providers are logged and skipped — a SearXNG
// failure must not abort a DuckDuckGo fallback.
type MultiWebSearcher struct {
	providers []scriptports.WebSearchProvider
	log       *zap.Logger
}

// NewMultiWebSearcher creates a multi-provider searcher. Provider order
// defines the fallback chain: the first provider is tried first; if its
// results are insufficient, the next provider fires.
func NewMultiWebSearcher(log *zap.Logger, providers ...scriptports.WebSearchProvider) *MultiWebSearcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &MultiWebSearcher{providers: providers, log: log}
}

// Search tries each provider in order, merging all results. This is the
// simple merge path — no escalation logic, no subject awareness.
// Subject-aware per-provider escalation lives in the
// ResearchSearchCoordinator (internal/app), which drives providers
// directly and consumes this searcher only as a dumb fallback path.
func (m *MultiWebSearcher) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	if m == nil || len(m.providers) == 0 {
		return nil, nil
	}
	var all []scriptports.WebSearchHit
	for _, p := range m.providers {
		hits, err := p.Search(ctx, query, limit)
		if err != nil {
			if m.log != nil {
				m.log.Warn("multi-provider search: provider error, continuing fallback",
					zap.String("provider", p.Name()),
					zap.String("query", query),
					zap.Error(err),
				)
			}
			continue
		}
		all = append(all, hits...)
	}
	return DeduplicateHits(all), nil
}

// ProviderNames returns the names of all registered providers in order.
func (m *MultiWebSearcher) ProviderNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, len(m.providers))
	for i, p := range m.providers {
		names[i] = p.Name()
	}
	return names
}

// HasProvider reports whether a provider with the given name is registered.
func (m *MultiWebSearcher) HasProvider(name string) bool {
	if m == nil {
		return false
	}
	for _, p := range m.providers {
		if strings.EqualFold(p.Name(), name) {
			return true
		}
	}
	return false
}

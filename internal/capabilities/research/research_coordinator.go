// Package app — research_coordinator.go provides the ResearchSearchCoordinator,
// the subject-aware search orchestrator for multi-provider research.
// It sits above the provider registry (SearXNG, DuckDuckGo, future) and
// below the page fetcher (evidence collection).
//
// Architecture:
//
//	IdentityResolver → QueryPlanner → ResearchSearchCoordinator
//	    ├── provider registry (SearXNG, DuckDuckGo, future)
//	    ├── provider search
//	    ├── URL normalization + cross-provider dedup
//	    ├── SubjectFilter (identity-aware relevance: RequiredTerms/ExcludedTerms)
//	    └── Escalation (next provider when the subject-valid pool is insufficient)
//	↓
//	page fetch → validate → quality gate → EvidencePack
//
// Escalation: for each query (up to maxQueries), providers are tried in
// registry order. After every provider call the subject-valid pool is
// merged and deduplicated; the search stops as soon as the pool reaches
// targetPool. A provider that errors or times out is logged and skipped —
// it must not abort a DuckDuckGo fallback.
//
// The MultiWebSearcher stays deliberately subject-unaware (merge/dedup/
// errors); this coordinator owns provider selection and subject-aware
// escalation above it.
package research

import (
	"context"
	"net/url"
	"strings"
	"unicode"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/webresearch"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
	"golang.org/x/text/unicode/norm"
)

// researchDefaultTargetPool is the default subject-valid pool size used
// when SearchWithFallback is called with targetPool <= 0. The composition
// root overrides it via SetTargetPool(cfg.External.ResearchTargetPoolSize).
const researchDefaultTargetPool = 8

// researchDefaultMaxQueries bounds planner-generated queries when the
// caller passes none (mirrors researchDefaultMaxQueries in the usecase).
const researchDefaultMaxQueries = 4

// ResearchSearchCoordinator orchestrates subject-aware search with
// multi-provider fallback. It resolves identity, plans queries, searches
// across providers (registry order), filters by subject relevance, and
// escalates to the next provider when the pool is insufficient.
type ResearchSearchCoordinator struct {
	resolver          *SubjectIdentityAdapter
	planner           *QueryPlannerAdapter
	providers         []scriptports.WebSearchProvider
	defaultTargetPool int
	log               *zap.Logger
}

// SubjectIdentityAdapter wraps the usecase.SubjectIdentityResolver for
// composition-level use.
type SubjectIdentityAdapter struct {
	Resolve func(subject string) scriptpkg.SubjectIdentity
}

// QueryPlannerAdapter wraps the usecase.QueryPlanner for composition-level use.
type QueryPlannerAdapter struct {
	FullPlan func(identity scriptpkg.SubjectIdentity, maxQueries int) []string
}

// NewResearchSearchCoordinator creates a coordinator with the given
// identity resolver, query planner, and provider registry. Provider
// order defines the fallback chain: the first provider is primary, the
// rest fire only when the subject-valid pool is insufficient.
func NewResearchSearchCoordinator(
	resolver *SubjectIdentityAdapter,
	planner *QueryPlannerAdapter,
	providers []scriptports.WebSearchProvider,
	log *zap.Logger,
) *ResearchSearchCoordinator {
	if log == nil {
		log = zap.NewNop()
	}
	return &ResearchSearchCoordinator{
		resolver:          resolver,
		planner:           planner,
		providers:         providers,
		defaultTargetPool: researchDefaultTargetPool,
		log:               log,
	}
}

// SetTargetPool overrides the default subject-valid pool size used when
// SearchWithFallback is called with targetPool <= 0.
func (c *ResearchSearchCoordinator) SetTargetPool(n int) {
	if n > 0 {
		c.defaultTargetPool = n
	}
}

// SearchResult carries a subject-valid search hit with metadata.
type SearchResult struct {
	Hit        scriptports.WebSearchHit
	Provider   string
	QueryLevel int
}

// SearchWithFallback performs subject-aware search with escalation.
// For each query it tries the providers in registry order, applying the
// subject filter after every provider call and stopping as soon as the
// deduplicated subject-valid pool reaches targetPool.
//
// A provider error or timeout is logged and skipped (fallback). A
// canceled/deadline-exceeded context stops the search early and returns
// whatever pool was collected.
//
// Returns the merged, deduplicated, subject-filtered pool.
func (c *ResearchSearchCoordinator) SearchWithFallback(
	ctx context.Context,
	subject string,
	queries []string,
	targetPool int,
) []SearchResult {
	if c == nil || c.resolver == nil || len(c.providers) == 0 {
		return nil
	}
	if targetPool <= 0 {
		targetPool = c.defaultTargetPool
	}
	identity := c.resolver.Resolve(subject)
	subjectFilter := buildSubjectFilter(identity)
	if len(queries) == 0 && c.planner != nil {
		queries = c.planner.FullPlan(identity, researchDefaultMaxQueries)
	}

	var pool []SearchResult
	for queryLevel, query := range queries {
		if ctx.Err() != nil || len(pool) >= targetPool {
			break
		}
		for providerIndex, provider := range c.providers {
			if ctx.Err() != nil || len(pool) >= targetPool {
				break
			}
			if provider == nil {
				continue
			}
			hits, err := provider.Search(ctx, query, 10)
			if err != nil {
				if c.log != nil {
					c.log.Warn("coordinator: provider search error, continuing fallback",
						zap.Int("query_level", queryLevel),
						zap.String("provider", provider.Name()),
						zap.String("query", query),
						zap.Error(err),
					)
				}
				continue
			}
			var accepted []SearchResult
			for _, hit := range hits {
				if len(pool)+len(accepted) >= targetPool {
					break
				}
				if !subjectFilter(hit.Title + " " + hit.Content) {
					if c.log != nil {
						c.log.Debug("coordinator: subject-filter rejected",
							zap.String("provider", provider.Name()),
							zap.String("url", hit.URL),
							zap.String("title", hit.Title),
						)
					}
					continue
				}
				accepted = append(accepted, SearchResult{
					Hit:        hit,
					Provider:   provider.Name(),
					QueryLevel: queryLevel,
				})
			}
			merged := len(pool)
			pool = append(pool, accepted...)
			pool = deduplicatePool(pool)
			if c.log != nil {
				c.log.Info("coordinator: provider search",
					zap.Int("query_level", queryLevel),
					zap.String("provider", provider.Name()),
					zap.Int("raw", len(hits)),
					zap.Int("subject_valid", len(accepted)),
					zap.Int("merged", merged+len(accepted)),
					zap.Int("pool", len(pool)),
					zap.Bool("fallback", providerIndex > 0),
				)
			}
			if len(pool) >= targetPool {
				if c.log != nil {
					c.log.Info("coordinator: target pool reached",
						zap.Int("target_pool", targetPool),
						zap.Int("pool", len(pool)),
					)
				}
				break
			}
		}
	}
	return pool
}

// buildSubjectFilter creates a subject relevance filter function from
// a SubjectIdentity. It checks excluded terms first (reject), then
// required terms (accept on first match), and falls back to
// diacritic-normalized token overlap on the canonical name. All matching
// is NFD-normalized so "Canelo Álvarez" matches "Canelo Alvarez".
func buildSubjectFilter(identity scriptpkg.SubjectIdentity) func(text string) bool {
	required := normalizeFilterTerms(identity.RequiredTerms)
	excluded := normalizeFilterTerms(identity.ExcludedTerms)
	canonicalTokens := identityFilterTokens(identity.CanonicalName)
	return func(text string) bool {
		if text == "" {
			return false
		}
		lower := stripFilterMarks(norm.NFD.String(strings.ToLower(text)))
		// Check excluded terms first — if any appear, reject.
		for _, excl := range excluded {
			if strings.Contains(lower, excl) {
				return false
			}
		}
		// Check required terms — at least one must appear.
		if len(required) > 0 {
			for _, req := range required {
				if strings.Contains(lower, req) {
					return true
				}
			}
			return false
		}
		// No required terms → use token-based identity matching.
		textTokens := identityFilterTokens(lower)
		for token := range canonicalTokens {
			if _, ok := textTokens[token]; ok {
				return true
			}
		}
		return len(canonicalTokens) == 0
	}
}

// normalizeFilterTerms lowercases, NFD-normalizes, and strips combining
// marks from identity terms so both sides of a Contains() match agree on
// diacritics ("duran" == "durán" == "Durán").
func normalizeFilterTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		out = append(out, stripFilterMarks(norm.NFD.String(strings.ToLower(strings.TrimSpace(term)))))
	}
	return out
}

// identityFilterTokens splits text into diacritic-normalized tokens
// (min length 3) for canonical-name overlap matching.
func identityFilterTokens(text string) map[string]struct{} {
	text = stripFilterMarks(norm.NFD.String(strings.ToLower(strings.TrimSpace(text))))
	result := make(map[string]struct{})
	for _, word := range strings.Fields(text) {
		word = strings.Trim(word, ".,;:()\"'")
		if len(word) >= 3 {
			result[word] = struct{}{}
		}
	}
	return result
}

// stripFilterMarks removes Unicode combining marks (NFD decomposition
// leaves accented letters as base + mark).
func stripFilterMarks(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Mn, r) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// deduplicatePool removes duplicate search results by normalized URL
// (via webresearch.NormalizeWebURL: lowercase host, strip www, drop
// tracking params, strip fragment), then by same host + exactly equal
// normalized title. The first occurrence wins, preserving its provider
// and query-level metadata.
func deduplicatePool(pool []SearchResult) []SearchResult {
	seenURL := make(map[string]struct{}, len(pool))
	seenTitle := make(map[string]struct{}, len(pool))
	var result []SearchResult
	for _, res := range pool {
		norm, err := webresearch.NormalizeWebURL(res.Hit.URL)
		if err != nil {
			continue
		}
		if _, ok := seenURL[norm]; ok {
			continue
		}
		seenURL[norm] = struct{}{}
		host := ""
		if u, err := url.Parse(norm); err == nil {
			host = u.Host
		}
		title := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(res.Hit.Title)), " "))
		if title != "" {
			key := host + "||" + title
			if _, ok := seenTitle[key]; ok {
				continue
			}
			seenTitle[key] = struct{}{}
		}
		result = append(result, res)
	}
	return result
}

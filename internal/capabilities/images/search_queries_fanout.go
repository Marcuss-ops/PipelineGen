package images

import (
	"context"
	"errors"
	"fmt"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"strings"
	"sync"

	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

var errFirstHit = errors.New("storage_search: first hit wins abort")

type retrievalBackend struct {
	name string
	fn   func(ctx context.Context) (imgURL, pageURL string)
}

type firstHitCollector struct {
	mu      sync.Mutex
	won     bool
	imgURL  string
	source  string
	pageURL string
}

func (c *firstHitCollector) record(imgURL, source, pageURL string) bool {
	if imgURL == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.won {
		return false
	}
	c.won = true
	c.imgURL, c.source, c.pageURL = imgURL, source, pageURL
	return true
}

func (c *firstHitCollector) result() (string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imgURL, c.source, c.pageURL
}

func fanOutRetrieval(ctx context.Context, log *zap.Logger, backends []retrievalBackend) (string, string, string) {
	if len(backends) == 0 {
		return "", "", ""
	}
	group, gctx := concurrent.WithContext(ctx)
	col := &firstHitCollector{}
	for _, b := range backends {
		b := b
		group.Go(b.name, func() error {
			if gctx.Err() != nil {
				return gctx.Err()
			}
			u, p := b.fn(gctx)
			if col.record(u, b.name, p) {
				return errFirstHit
			}
			return nil
		})
	}
	// errFirstHit is the intentional winner signal; context cancellation is a
	// normal miss. Anything else escaping the fan-out is a real backend
	// failure and must be visible, never silently swallowed.
	if waitErr := group.Wait(); waitErr != nil &&
		!errors.Is(waitErr, errFirstHit) &&
		!errors.Is(waitErr, context.Canceled) &&
		!errors.Is(waitErr, context.DeadlineExceeded) {
		log.Warn("retrieval fan-out backend failed",
			zap.Int("backends", len(backends)),
			zap.Error(waitErr),
		)
	}
	img, src, page := col.result()
	if img != "" {
		log.Info("retrieval fan-out winner selected", zap.String("source", src), zap.String("url", img), zap.Int("backends", len(backends)))
	} else {
		log.Warn("retrieval fan-out exhausted — no hit", zap.Int("backends", len(backends)))
	}
	return img, src, page
}

func (s *ImageStorageService) runRetrievalFallbackForProvider(ctx context.Context, query, lang string, provider detail.ImageProvider) (imgURL, source, pageURL string) {
	if provider != "" {
		s.log.Info("explicit retrieved provider selected", zap.String("provider", string(provider)), zap.String("query", query))
		if s.retrievalRegistry == nil {
			s.log.Warn("explicit retrieved provider skipped: retrieval registry is not wired", zap.String("provider", string(provider)), zap.String("query", query))
			return "", "", ""
		}
		p := s.retrievalRegistry.SearchByName(provider)
		if p == nil {
			s.log.Warn("explicit retrieved provider not found in registry", zap.String("provider", string(provider)), zap.String("query", query))
			return "", "", ""
		}
		results, err := p.Search(ctx, query, retrieved.RetrievalSearchOptions{Lang: lang})
		if err != nil {
			s.log.Warn("explicit retrieved provider search failed", zap.String("provider", string(provider)), zap.String("query", query), zap.Error(err))
			return "", "", ""
		}
		if len(results) == 0 {
			s.log.Debug("explicit retrieved provider returned no results", zap.String("provider", string(provider)), zap.String("query", query))
			return "", "", ""
		}
		hit := results[0]
		if hit.PreviewURL == "" {
			return "", "", ""
		}
		pageURL = hit.PageURL
		if pageURL == "" {
			pageURL = hit.PreviewURL
		}
		return hit.PreviewURL, string(p.Name()), pageURL
	}

	// Query recession (fanout=0 fix): a provider can answer empty for a flaky,
	// rate-limited or over-narrow primary query. Before declaring the scene
	// imageless, recede through deterministically relaxed variants of the same
	// query. The ladder is bounded so a genuinely imageless subject does not
	// multiply provider calls without limit.
	rungs := retrieved.RelaxImageQuery(query)
	if len(rungs) == 0 {
		return "", "", ""
	}
	if len(rungs) > maxImageQueryRecessionRungs {
		rungs = rungs[:maxImageQueryRecessionRungs]
	}
	for i, rung := range rungs {
		imgURL, source, pageURL := s.runRetrievalRung(ctx, rung, lang)
		if imgURL != "" {
			if i > 0 {
				s.log.Info("retrieval receded to a relaxed query",
					zap.String("original_query", query), zap.String("relaxed_query", rung))
			}
			return imgURL, source, pageURL
		}
	}
	return "", "", ""
}

// maxImageQueryRecessionRungs bounds how many relaxed query variants a single
// retrieval attempt may try. Three covers the useful cases (instruction
// prefix, clause strip, one truncation) without turning a miss into a burst.
const maxImageQueryRecessionRungs = 3

// runRetrievalRung executes ONE query against the shared retrieval surface,
// preferring the concurrent quality-ranked selection (SearchBest) over the
// legacy first-hit-wins fan-out. The registry path uses SearchBest; the legacy
// no-registry path keeps the parallel engine fan-out.
func (s *ImageStorageService) runRetrievalRung(ctx context.Context, query, lang string) (string, string, string) {
	if s.retrievalRegistry != nil {
		results, err := s.retrievalRegistry.SearchBest(ctx, query, retrieved.RetrievalSearchOptions{Lang: lang})
		if err != nil {
			s.log.Warn("retrieval best search failed", zap.String("query", query), zap.Error(err))
			return "", "", ""
		}
		if len(results) == 0 {
			return "", "", ""
		}
		hit := results[0]
		if hit.PreviewURL == "" {
			return "", "", ""
		}
		pageURL := hit.PageURL
		if pageURL == "" {
			pageURL = hit.PreviewURL
		}
		return hit.PreviewURL, string(hit.Provider), pageURL
	}
	return s.legacyRetrievalFanOut(ctx, query, lang)
}

// retrievalCandidate is one downloadable hit with the provenance the ingest
// path needs. The provider chain can return several candidates; the single
// image path tries them in order instead of failing the whole request when the
// first origin refuses the download (402/403/timeout).
type retrievalCandidate struct {
	URL     string
	Source  string
	PageURL string
}

// maxRetrievalCandidateAttempts bounds how many origins one single-image
// request may try. The first few DDG hits normally include at least one
// reachable origin; ten keeps the request bounded without retrying forever.
const maxRetrievalCandidateAttempts = 10

func retrievalCandidatesFromResults(results []retrieved.RetrievalSearchResult, fallback detail.ImageProvider) []retrievalCandidate {
	out := make([]retrievalCandidate, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, hit := range results {
		imageURL := strings.TrimSpace(hit.PreviewURL)
		if imageURL == "" {
			imageURL = strings.TrimSpace(hit.ImageURL)
		}
		if imageURL == "" {
			imageURL = strings.TrimSpace(hit.ThumbnailURL)
		}
		if imageURL == "" {
			continue
		}
		key := strings.ToLower(imageURL)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		source := string(hit.Provider)
		if source == "" {
			source = string(fallback)
		}
		pageURL := strings.TrimSpace(hit.PageURL)
		if pageURL == "" {
			pageURL = strings.TrimSpace(hit.SourcePageURL)
		}
		if pageURL == "" {
			pageURL = imageURL
		}
		out = append(out, retrievalCandidate{URL: imageURL, Source: source, PageURL: pageURL})
		if len(out) >= maxRetrievalCandidateAttempts {
			break
		}
	}
	return out
}

// runRetrievalCandidatesForProvider is the candidate-producing form of
// runRetrievalFallbackForProvider. It preserves the same provider selection and
// query-recession semantics, but returns every ranked hit so the caller can
// fail over per origin rather than per query.
func (s *ImageStorageService) runRetrievalCandidatesForProvider(ctx context.Context, query, lang string, provider detail.ImageProvider) []retrievalCandidate {
	if provider != "" {
		if s.retrievalRegistry == nil {
			s.log.Warn("explicit retrieved provider skipped: retrieval registry is not wired", zap.String("provider", string(provider)), zap.String("query", query))
			return nil
		}
		p := s.retrievalRegistry.SearchByName(provider)
		if p == nil {
			s.log.Warn("explicit retrieved provider not found in registry", zap.String("provider", string(provider)), zap.String("query", query))
			return nil
		}
		results, err := p.Search(ctx, query, retrieved.RetrievalSearchOptions{Lang: lang})
		if err != nil {
			s.log.Warn("explicit retrieved provider search failed", zap.String("provider", string(provider)), zap.String("query", query), zap.Error(err))
			return nil
		}
		return retrievalCandidatesFromResults(results, p.Name())
	}

	rungs := retrieved.RelaxImageQuery(query)
	if len(rungs) == 0 {
		return nil
	}
	if len(rungs) > maxImageQueryRecessionRungs {
		rungs = rungs[:maxImageQueryRecessionRungs]
	}
	for i, rung := range rungs {
		candidates := s.retrievalCandidatesForRung(ctx, rung, lang)
		if len(candidates) > 0 {
			if i > 0 {
				s.log.Info("retrieval receded to a relaxed query",
					zap.String("original_query", query), zap.String("relaxed_query", rung))
			}
			return candidates
		}
	}
	return nil
}

func (s *ImageStorageService) retrievalCandidatesForRung(ctx context.Context, query, lang string) []retrievalCandidate {
	if s.retrievalRegistry != nil {
		results, err := s.retrievalRegistry.SearchBest(ctx, query, retrieved.RetrievalSearchOptions{Lang: lang})
		if err != nil {
			s.log.Warn("retrieval best search failed", zap.String("query", query), zap.Error(err))
			return nil
		}
		return retrievalCandidatesFromResults(results, "")
	}
	imgURL, source, pageURL := s.legacyRetrievalFanOut(ctx, query, lang)
	if imgURL == "" {
		return nil
	}
	return []retrievalCandidate{{URL: imgURL, Source: source, PageURL: pageURL}}
}

// legacyRetrievalFanOut is the no-registry engine cascade (Wikipedia →
// SearXNG → DuckDuckGo). It is only reached when the canonical provider
// registry is unwired; the registry path runs through SearchBest instead.
func (s *ImageStorageService) legacyRetrievalFanOut(ctx context.Context, query, lang string) (string, string, string) {
	backends := []retrievalBackend{
		{name: "wikipedia", fn: func(c context.Context) (string, string) {
			img, title := s.searchWikipedia(c, query, lang)
			if img == "" {
				return "", ""
			}
			pURL := ""
			if title != "" {
				pURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(title, " ", "_"))
			}
			return img, pURL
		}},
		{name: "searxng", fn: func(c context.Context) (string, string) {
			img := s.searchSearXNGImages(c, query)
			if img == "" {
				return "", ""
			}
			return img, img
		}},
		{name: "duckduckgo", fn: func(c context.Context) (string, string) {
			img := s.searchDDGWide(c, query)
			if img == "" {
				return "", ""
			}
			return img, img
		}},
	}
	return fanOutRetrieval(ctx, s.log, backends)
}

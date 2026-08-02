// Package images — search_queries_fanout.go is the parallel fan-out
// subsystem extracted from search_queries.go (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3).
//
// Owns: errFirstHit, retrievalBackend, firstHitCollector, fanOutRetrieval, runRetrievalFallback.
package images

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// errFirstHit is the synthetic sentinel returned by the winning
// goroutine inside fanOutRetrieval. pkg/concurrent.Group.WithContext's
// first-error-wins treats it like any other non-nil error and cancels
// the child context; siblings observe ctx.Done() and abort cleanly.
// Local to this file — no leaf-pkg modification required.
var errFirstHit = errors.New("storage_search: first hit wins abort")

// retrievalBackend is the uniform shape for an image-search backend
// participating in the parallel fan-out. Returning a non-empty imgURL
// from fn is a "hit"; the first writer wins and cancels siblings via
// errFirstHit. fn MUST honour ctx.Done() for the early-exit contract.
type retrievalBackend struct {
	name string
	fn   func(ctx context.Context) (imgURL, pageURL string)
}

// firstHitCollector is a mutex-protected single-winner cache. The
// first goroutine to record a non-empty (imgURL, pageURL) tuple
// wins; later records are no-ops.
type firstHitCollector struct {
	mu      sync.Mutex
	won     bool
	imgURL  string
	source  string
	pageURL string
}

// record atomically stores the first non-empty hit; returns true if
// this call was the writer, false otherwise (caller was a slow loser
// or supplied an empty hit).
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

// result returns the winner's tuple (or all-empty if no winner).
func (c *firstHitCollector) result() (string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.imgURL, c.source, c.pageURL
}

// fanOutRetrieval runs backends in parallel via pkg/concurrent.Group
// and returns the first non-empty (imgURL, source, pageURL) tuple.
// The winner returns errFirstHit so the group's first-error-wins
// cancels the child context; siblings check ctx.Err() and exit.
//
// Logging policy (per B5 thinker's logs-only-on-outcome counter):
// emits EXACTLY ONE log line at end — Info "winner selected" or
// Warn "no hit" — never per-backend. Per-backend diagnostics live
// inside each backend's fn (sealed inside its goroutine) so the
// helper itself stays deterministic at the log surface.
func fanOutRetrieval(ctx context.Context, log *zap.Logger, backends []retrievalBackend) (string, string, string) {
	if len(backends) == 0 {
		return "", "", ""
	}
	group, gctx := concurrent.WithContext(ctx)
	col := &firstHitCollector{}

	for _, b := range backends {
		b := b // closure capture per iteration (Go 1.22+ no longer needed, kept explicit for clarity)
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

	_ = group.Wait() // errFirstHit expected; actual result lives in col
	img, src, page := col.result()
	if img != "" {
		log.Info("retrieval fan-out winner selected",
			zap.String("source", src),
			zap.String("url", img),
			zap.Int("backends", len(backends)),
		)
	} else {
		log.Warn("retrieval fan-out exhausted — no hit",
			zap.Int("backends", len(backends)),
		)
	}
	return img, src, page
}

// runRetrievalFallback (Step 8 + B5 fan-out) walks the retrieval
// backends in PARALLEL via fanOutRetrieval and returns the first
// non-empty hit. The 4 legacy backends (Wikipedia / SearXNG / DDG
// in the no-Registry path) plus the Step-8 retrieval-registry
// providers all fan out together. Returns (imgURL, source, pageURL)
// tuples aligned with the legacy cascade semantics:
//   - Wikipedia hit → source="wikipedia", pageURL points at the wiki page
//   - SearXNG hit    → source="searxng", pageURL=imgURL
//   - DuckDuckGo hit → source="duckduckgo", pageURL=imgURL
//   - Drive hit      → source="drive", pageURL=imgURL
//   - registry-only   → source from registry.Provider, pageURL from registry
//
// When the registry is nil (tests that pre-date Step 8), the
// 3-backend legacy path is used (B5 still parallelizes the 3).
//
// B5 SSOT refactor (PR-IMAGES-AI-VS-NORMAL-PLAN, July 2026):
// replaces the pre-B5 sequential cascade Wikipedia → SearXNG →
// DDG → Registry with 4-way concurrent fan-out. Worst-case
// latency drops from ~800ms (4 backends × 200ms, registry last)
// to ~200ms (parallel — slowest wins). Cancellable, panic-safe
// via pkg/concurrent.Group's per-goroutine panic-recover wrapper.
func (s *ImageStorageService) runRetrievalFallback(ctx context.Context, query, lang string) (imgURL, source, pageURL string) {
	return s.runRetrievalFallbackForProvider(ctx, query, lang, "")
}

// runRetrievalFallbackForProvider resolves an explicit provider from the
// shared registry when requested. The empty provider preserves the normal
// fan-out behavior. Explicit selection is used by live canaries and keeps
// provider verification independent from whichever fallback wins first.
func (s *ImageStorageService) runRetrievalFallbackForProvider(ctx context.Context, query, lang string, provider asset.ImageProvider) (imgURL, source, pageURL string) {
	if provider != "" {
		s.log.Info("explicit retrieved provider selected", zap.String("provider", string(provider)), zap.String("query", query))
		if s.retrievalRegistry == nil {
			return "", "", ""
		}
		p := s.retrievalRegistry.SearchByName(provider)
		if p == nil {
			return "", "", ""
		}
		results, err := p.Search(ctx, query, retrieved.RetrievalSearchOptions{Lang: lang})
		if err != nil || len(results) == 0 {
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

	var backends []retrievalBackend

	if s.retrievalRegistry == nil {
		// ── Legacy 3-backend path (pre-Registry tests) ──
		// Each closure runs inside its own goroutine via
		// fanOutRetrieval; ctx passed via parameter (searchWikipedia
		// is ctx-agnostic in its pre-Step-8 signature so we pass
		// gctx indirectly through fanOutRetrieval's child ctx).
		backends = []retrievalBackend{
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
	} else {
		// ── Step-8 registry path: fan out across all registered
		// providers (typically Wikipedia + SearXNG + DDG + Drive).
		// retrievalRegistry.Providers returns a defensive copy so
		// range is safe without aliasing.
		for _, p := range s.retrievalRegistry.Providers() {
			p := p // closure capture
			backends = append(backends, retrievalBackend{
				name: string(p.Name()),
				fn: func(c context.Context) (string, string) {
					if c.Err() != nil {
						return "", ""
					}
					res, _ := p.Search(c, query, retrieved.RetrievalSearchOptions{Lang: lang})
					if len(res) == 0 {
						return "", ""
					}
					hit := res[0]
					pURL := hit.PageURL
					if pURL == "" {
						pURL = hit.PreviewURL
					}
					return hit.PreviewURL, pURL
				},
			})
		}
	}

	img, src, page := fanOutRetrieval(ctx, s.log, backends)
	return img, src, page
}

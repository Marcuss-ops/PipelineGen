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
	_ = group.Wait()
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

	var backends []retrievalBackend
	if s.retrievalRegistry == nil {
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
		for _, p := range s.retrievalRegistry.Providers() {
			p := p
			backends = append(backends, retrievalBackend{
				name: string(p.Name()),
				fn: func(c context.Context) (string, string) {
					if c.Err() != nil {
						return "", ""
					}
					res, err := p.Search(c, query, retrieved.RetrievalSearchOptions{Lang: lang})
					if err != nil {
						s.log.Warn("retrieved provider search failed", zap.String("provider", string(p.Name())), zap.String("query", query), zap.Error(err))
						return "", ""
					}
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
	return fanOutRetrieval(ctx, s.log, backends)
}

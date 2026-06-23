package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/classifier"
)

// youtubeCategoryCache implements classifier.CategoryCache backed by the cache service.
type youtubeCategoryCache struct {
	svc *Service
}

func (c *youtubeCategoryCache) Get(ctx context.Context, title string) (string, bool) {
	if c.svc.cache == nil {
		return "", false
	}
	return c.svc.cache.GetCategory(ctx, title)
}

func (c *youtubeCategoryCache) Set(ctx context.Context, title, category string) error {
	// Best-effort: category cache is a performance optimization, not a correctness
	// requirement. Nil cache service means the classification won't be persisted,
	// but the caller still receives the category result for immediate use.
	if c.svc.cache == nil {
		return nil
	}
	c.svc.cache.SetCategory(ctx, title, category)
	return nil
}

// classifyCategory classifies the video title using the shared classifier with SQLite cache.
func (s *Service) classifyCategory(ctx context.Context, title string) string {
	if s.ollama == nil {
		return "general"
	}
	return classifier.CachedClassify(ctx, s.log, s.ollama, title, classifier.Options{
		DataDir:          s.cfg.Storage.DataDir,
		Model:            s.cfg.External.OllamaModel,
		FallbackCategory: "general",
		ExcludeCategories: []string{
			"interviews", "general", "other", "clips", "youtube", "videos",
		},
		EnsureCategories:  []string{"rap", "music"},
		DefaultCategories: []string{"boxe", "crime", "discovery", "rap", "music"},
		Cache:             &youtubeCategoryCache{svc: s},
		Semaphore:         s.ollamaSem,
	})
}

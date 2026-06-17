package youtube

import (
	"context"
	"fmt"

	"velox/go-master/internal/media/classifier"
)

// youtubeCategoryCache implements classifier.CategoryCache backed by SQLite.
type youtubeCategoryCache struct {
	svc *Service
}

func (c *youtubeCategoryCache) Get(ctx context.Context, title string) (string, bool) {
	if c.svc.clipsRepo == nil || c.svc.clipsRepo.DB() == nil {
		return "", false
	}
	var category string
	err := c.svc.clipsRepo.DB().QueryRowContext(ctx, "SELECT category FROM youtube_category_cache WHERE video_title = ?", title).Scan(&category)
	if err == nil {
		return category, true
	}
	return "", false
}

func (c *youtubeCategoryCache) Set(ctx context.Context, title, category string) error {
	if c.svc.clipsRepo == nil || c.svc.clipsRepo.DB() == nil {
		return fmt.Errorf("clipsRepo not initialized")
	}
	_, err := c.svc.clipsRepo.DB().ExecContext(ctx, "INSERT OR REPLACE INTO youtube_category_cache (video_title, category, cached_at) VALUES (?, ?, datetime('now'))", title, category)
	return err
}

// classifyCategory classifies the video title using the shared classifier with SQLite cache.
func (s *Service) classifyCategory(ctx context.Context, title string) string {
	if s.ollamaClient == nil {
		return "general"
	}
	return classifier.CachedClassify(ctx, s.log, s.ollamaClient, title, classifier.Options{
		DataDir:          s.cfg.Storage.DataDir,
		Model:            s.cfg.External.OllamaModel,
		FallbackCategory: "general",
		ExcludeCategories: []string{
			"interviews", "general", "other", "clips", "youtube", "videos",
		},
		EnsureCategories:  []string{"rap", "music"},
		DefaultCategories: []string{"boxe", "crime", "discovery", "rap", "music"},
		Cache:             &youtubeCategoryCache{svc: s},
		Semaphore:         ollamaSem,
	})
}

// Package images — storage_bridge.go exposes the StorageBridge
// surface that the retrieved/ subpackage providers depend on.
package images

import (
	"context"
	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
)

func (s *ImageStorageService) SearchBySlug(ctx context.Context, slug string, limit int) []string {
	if s == nil || s.repo == nil {
		return nil
	}
	assets, err := s.repo.ListImagesBySubject(ctx, slug)
	if err != nil || len(assets) == 0 {
		return nil
	}
	if limit > 0 && len(assets) > limit {
		assets = assets[:limit]
	}
	out := make([]string, 0, len(assets))
	for i := range assets {
		if assets[i].PathRel != "" {
			out = append(out, assets[i].PathRel)
		}
	}
	return out
}

func (s *ImageStorageService) SearchWikipedia(ctx context.Context, query, lang string) (string, string) {
	if s == nil {
		return "", ""
	}
	return s.searchWikipedia(ctx, query, lang)
}

func (s *ImageStorageService) SearchWikimediaCommons(ctx context.Context, query string) retrieved.RetrievalSearchResult {
	if s == nil {
		return retrieved.RetrievalSearchResult{}
	}
	return s.searchWikimediaCommons(ctx, query)
}

func (s *ImageStorageService) SearchSearXNGImages(ctx context.Context, query string) string {
	if s == nil {
		return ""
	}
	return s.searchSearXNGImages(ctx, query)
}

func (s *ImageStorageService) SearchSearXNGImagesMany(ctx context.Context, query string, limit int) []retrieved.RetrievalSearchResult {
	if s == nil {
		return nil
	}
	return s.searchSearXNGImagesMany(ctx, query, limit)
}

func (s *ImageStorageService) SearchDDGWide(ctx context.Context, query string) string {
	if s == nil {
		return ""
	}
	return s.searchDDGWide(ctx, query)
}

func (s *ImageStorageService) SearchDDGWideMany(ctx context.Context, query string, limit int) []string {
	if s == nil {
		return nil
	}
	return s.searchDDGWideMany(ctx, query, limit)
}

var _ retrieved.StorageBridge = (*ImageStorageService)(nil)

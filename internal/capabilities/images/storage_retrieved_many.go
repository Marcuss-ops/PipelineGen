package images

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// MaxRetrievedImageBatch bounds one synchronous multi-image request. The
// bound protects the API from an accidental unbounded download while still
// covering the normal image-search use case.
const MaxRetrievedImageBatch = 100

// SearchAndDownloadManyDetailedFromProvider retrieves up to limit distinct
// images from the selected provider. The multi-result path is intentionally
// explicit: it currently supports DuckDuckGo, whose continuation API returns
// the original image URLs used by the downloader.
func (s *ImageStorageService) SearchAndDownloadManyDetailedFromProvider(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string, provider detail.ImageProvider, limit int) ([]*SearchResult, error) {
	if limit < 1 || limit > MaxRetrievedImageBatch {
		return nil, fmt.Errorf("image result limit must be between 1 and %d", MaxRetrievedImageBatch)
	}
	if provider == "" {
		provider = detail.ProviderDuckDuckGo
	}
	if provider != detail.ProviderDuckDuckGo {
		return nil, fmt.Errorf("multi-image retrieval provider %q is not supported", provider)
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("image query is empty")
	}
	if s == nil {
		return nil, fmt.Errorf("image storage service is nil")
	}

	urls := s.searchDDGWideMany(ctx, query, limit)
	results := make([]*SearchResult, 0, len(urls))
	for _, imgURL := range urls {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		if len(results) >= limit {
			break
		}

		provCtx := context.WithValue(ctx, SourceTypeKey, "retrieved")
		provCtx = context.WithValue(provCtx, RetrieverKey, string(provider))
		provCtx = context.WithValue(provCtx, SearchQueryKey, query)
		provCtx = context.WithValue(provCtx, ImageURLKey, imgURL)
		provCtx = context.WithValue(provCtx, PageURLKey, imgURL)
		license, author := defaultLicenseAndAuthor(ctx, string(provider))
		provCtx = context.WithValue(provCtx, LicenseKey, license)
		provCtx = context.WithValue(provCtx, AuthorKey, author)

		slug := subjectSlug
		if strings.TrimSpace(slug) == "" {
			slug = query
		}
		description := fmt.Sprintf("Image for %s found via %s", displayName, provider)
		imgAsset, err := s.downloadAndIngest(provCtx, slug, imgURL, slug, string(provider), query, description, tags)
		if err != nil || imgAsset == nil {
			// One inaccessible origin must not discard the other valid DDG
			// candidates. The caller receives the number actually acquired.
			continue
		}
		updatedJSON := detail.AppendImageProvenance(imgAsset.MetadataJSON, imgURL, imgURL, string(provider), query)
		if err := s.repo.UpdateImageMetadata(ctx, imgAsset.Hash, updatedJSON); err != nil {
			continue
		}
		imgAsset.MetadataJSON = updatedJSON
		results = append(results, &SearchResult{
			Asset:             imgAsset,
			CacheHit:          false,
			CacheSource:       "provider",
			RetrievalProvider: string(provider),
		})
	}
	return results, nil
}

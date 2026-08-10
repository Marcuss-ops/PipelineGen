package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Typed image-operation errors (FASE 2.3, July 2026) ───────────────
//
// Each sentinel wraps a specific failure category so callers can
// distinguish transient (retryable) from permanent failures without
// string-matching HTTP status codes.

// ErrImageNotFound is returned when the remote server responds with
// HTTP 404 — the image does not exist at the given URL. NOT retryable.
var ErrImageNotFound = errors.New("image: not found (HTTP 404)")

// ErrImageTransient is the canonical transient-error sentinel for image
// operations (429, 5xx, timeout, connection refused). Implements the
// retry.RetryableError interface (IsRetryable() bool → true) so
// retry.IsTransient recognises it at the typed layer without substring
// matching. Retryable with backoff.
type errImageTransient struct{}

func (e *errImageTransient) Error() string     { return "image: transient error (retryable)" }
func (e *errImageTransient) IsRetryable() bool { return true }

var ErrImageTransient error = &errImageTransient{}

// ErrImageInvalidResponse is returned when the HTTP response body is
// not a valid image or JSON (malformed, empty, wrong content-type).
// NOT retryable.
var ErrImageInvalidResponse = errors.New("image: invalid or corrupt response")

// ErrImageRepositoryUnavailable is returned when image ingestion cannot
// persist its canonical asset because the repository was not wired.
var ErrImageRepositoryUnavailable = errors.New("image: repository unavailable")

// ── Search & Download ─────────────────────────────────────────────────

// defaultLicenseAndAuthor resolves the canonical license and author for
// a given source through the provider descriptor registry. It removes the
// previous hardcoded `source == "wikipedia"` special case.
func defaultLicenseAndAuthor(ctx context.Context, source string) (license, author string) {
	if source == "" {
		return "Unknown", "Unknown"
	}
	if d, ok := asset.DefaultProviderRegistry().Match(source); ok {
		if d.LicenseResolver != nil {
			if l, err := d.LicenseResolver(ctx, nil); err == nil && l != "" {
				license = l
			}
		}
		if license == "" {
			license = d.DefaultRightsStatus
		}
		author = d.DefaultAuthor
		if author == "" {
			author = "Unknown"
		}
		return
	}
	return "Unknown", "Unknown"
}

// SearchAndDownload searches for an image locally and via web APIs.
func (s *ImageStorageService) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error) {
	out, err := s.SearchAndDownloadDetailed(ctx, subjectSlug, displayName, query, lang, tags)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Asset, nil
}

// SearchAndDownloadDetailed searches for an image locally and via web APIs
// and returns the canonical asset plus the trace needed for HTTP callers.
func (s *ImageStorageService) SearchAndDownloadDetailed(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*SearchResult, error) {
	return s.searchAndDownloadDetailed(ctx, subjectSlug, displayName, query, lang, tags, "")
}

// SearchAndDownloadDetailedFromProvider runs the canonical retrieved-image
// pipeline with one provider selected through the shared registry. It is
// intentionally a narrow diagnostic/canary seam: acquisition, validation,
// persistence, cache and metadata updates remain identical to the default
// fallback path.
func (s *ImageStorageService) SearchAndDownloadDetailedFromProvider(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string, provider asset.ImageProvider) (*SearchResult, error) {
	if provider == "" {
		return s.SearchAndDownloadDetailed(ctx, subjectSlug, displayName, query, lang, tags)
	}
	if s.retrievalRegistry == nil || s.retrievalRegistry.SearchByName(provider) == nil {
		return nil, fmt.Errorf("retrieved provider %q is not registered", provider)
	}
	return s.searchAndDownloadDetailed(ctx, subjectSlug, displayName, query, lang, tags, provider)
}

func (s *ImageStorageService) searchAndDownloadDetailed(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string, provider asset.ImageProvider) (*SearchResult, error) {
	slug := textutil.Slugify(subjectSlug)
	if slug == "" {
		slug = textutil.Slugify(query)
	}
	if lang == "" {
		lang = "it"
	}
	policySignature := s.retrievalPolicySignature()
	if provider != "" {
		policySignature += ":explicit:" + string(provider)
	}

	qLower := strings.ToLower(query)
	if qLower == "name" || qLower == "titolo" || len(query) < 2 {
		return nil, fmt.Errorf("invalid query term: %s", query)
	}

	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	// media_assets.metadata_json.subject_id is the durable cache key. Do not
	// gate this read on the optional/legacy subjects row: old databases can
	// reject subject creation because of a historical uuid constraint while
	// the image asset itself is already fully persisted.
	if images, listErr := s.repo.ListImagesBySubject(ctx, slug); listErr == nil && len(images) > 0 {
		if provider != "" {
			images = filterCachedImagesByProvider(images, provider)
		}
		if cached, score := selectBestCachedImageAsset(query, images); cached != nil {
			s.log.Info("Image cache hit from local database",
				zap.String("subject", slug),
				zap.String("cache_key", imageSearchCacheKey(query, lang, policySignature)),
				zap.String("cache_source", "database"),
				zap.String("retrieval_provider", string(cached.Provider)),
				zap.String("asset_id", cached.Hash),
				zap.Int("count", len(images)),
				zap.Int("cache_score", score),
			)
			retProvider := string(cached.Provider)
			if retProvider == "" || retProvider == "unknown" {
				var meta map[string]any
				if err := json.Unmarshal([]byte(cached.MetadataJSON), &meta); err == nil {
					if src, ok := meta["source_name"].(string); ok && src != "" {
						retProvider = src
					}
				}
			}
			return &SearchResult{
				Asset:             cached,
				CacheHit:          true,
				CacheSource:       "database",
				RetrievalProvider: retProvider,
			}, nil
		}
		s.log.Info("Images found in local database but no semantic cache hit",
			zap.String("subject", slug),
			zap.String("cache_key", imageSearchCacheKey(query, lang, policySignature)),
			zap.Int("count", len(images)),
		)
	}

	if subject == nil {
		subject = &asset.Subject{Slug: slug, DisplayName: displayName}
		_, err := s.repo.CreateSubject(ctx, subject)
		if err != nil {
			s.log.Warn("Ingest: subject might already exist", zap.String("slug", slug))
		}
	}

	key := imageSearchCacheKey(query, lang, policySignature)
	result, err, _ := s.dedup.Do(key, func() (any, error) {
		return s.searchAndDownloadInnerDetailed(ctx, slug, displayName, query, lang, tags, subject, provider)
	})
	if err != nil {
		return nil, err
	}
	if traced, ok := result.(*SearchResult); ok {
		return traced, nil
	}
	return nil, fmt.Errorf("singleflight: unexpected result type")
}

func (s *ImageStorageService) searchAndDownloadInnerDetailed(ctx context.Context, slug, displayName, query, lang string, tags []string, subject *asset.Subject, provider asset.ImageProvider) (*SearchResult, error) {
	s.log.Info("Disambiguating with Wikidata", zap.String("query", query), zap.String("lang", lang))
	wikiTitle, qid, _ := s.searchWikidata(query, lang)
	finalQuery := query
	if wikiTitle != "" {
		finalQuery = wikiTitle
		s.log.Info("Wikidata disambiguation successful", zap.String("original", query), zap.String("resolved", finalQuery), zap.String("qid", qid))
	} else {
		s.log.Warn("Wikidata disambiguation found nothing", zap.String("query", query))
	}

	// Step 8: route the network search through the RetrievalProviderRegistry.
	// Wikidata disambig above is preserved as it feeds the Wikipedia
	// canonical title rather than performing a network round-trip.
	imgURL, source, wikiURL := s.runRetrievalFallbackForProvider(ctx, finalQuery, lang, provider)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", finalQuery)
	}

	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	description := fmt.Sprintf("Image for %s found via %s", displayName, source)

	provCtx := context.WithValue(ctx, SourceTypeKey, "retrieved")
	provCtx = context.WithValue(provCtx, RetrieverKey, source)
	provCtx = context.WithValue(provCtx, SearchQueryKey, query)
	provCtx = context.WithValue(provCtx, ImageURLKey, imgURL)
	if wikiURL != "" {
		provCtx = context.WithValue(provCtx, PageURLKey, wikiURL)
	} else {
		provCtx = context.WithValue(provCtx, PageURLKey, imgURL)
	}
	license, author := defaultLicenseAndAuthor(ctx, source)
	provCtx = context.WithValue(provCtx, LicenseKey, license)
	provCtx = context.WithValue(provCtx, AuthorKey, author)

	imgAsset, err := s.downloadAndIngest(provCtx, slug, imgURL, slug, source, finalQuery, description, tags)
	if err == nil && imgAsset != nil {
		pageURL := ""
		if wikiURL != "" {
			pageURL = wikiURL
		}
		updatedJSON := asset.AppendImageProvenance(imgAsset.MetadataJSON, imgURL, pageURL, source, query)
		if finalQuery != "" && finalQuery != query {
			updatedJSON = asset.AppendImageMetadataField(updatedJSON, "resolved_query", finalQuery)
		}
		if updateErr := s.repo.UpdateImageMetadata(ctx, imgAsset.Hash, updatedJSON); updateErr != nil {
			s.log.Error("searchAndDownloadInner: UpdateImageMetadata failed", zap.Error(updateErr))
			return &SearchResult{
				Asset:             imgAsset,
				CacheHit:          false,
				CacheSource:       "provider",
				RetrievalProvider: source,
			}, fmt.Errorf("update image metadata: %w", updateErr)
		}
		imgAsset.MetadataJSON = updatedJSON
	}
	if err != nil || imgAsset == nil {
		return nil, err
	}

	return &SearchResult{
		Asset:             imgAsset,
		CacheHit:          false,
		CacheSource:       "provider",
		RetrievalProvider: source,
	}, nil
}

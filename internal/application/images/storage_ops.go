package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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

// ── Search & Download ─────────────────────────────────────────────────

// SearchAndDownload searches for an image locally and via web APIs.
func (s *ImageStorageService) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error) {
	slug := textutil.Slugify(subjectSlug)
	if slug == "" {
		slug = textutil.Slugify(query)
	}
	if lang == "" {
		lang = "it"
	}

	qLower := strings.ToLower(query)
	if qLower == "name" || qLower == "titolo" || len(query) < 2 {
		return nil, fmt.Errorf("invalid query term: %s", query)
	}

	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err == nil && subject != nil {
		if images, err := s.repo.ListImagesBySubject(ctx, subject.Slug); err == nil && len(images) > 0 {
			s.log.Info("Images found in local database", zap.String("subject", subject.Slug), zap.Int("count", len(images)))
			if len(images) > 1 {
				// Retrieval is a reproducible resolver: a repeated query with
				// the same catalog must not select a random asset.
				sort.SliceStable(images, func(i, j int) bool {
					if images[i].Hash != images[j].Hash {
						return images[i].Hash < images[j].Hash
					}
					return images[i].Provider < images[j].Provider
				})
				s.log.Info("Picking deterministic image from database", zap.String("asset_id", images[0].Hash), zap.Int("total", len(images)))
			}
			return &images[0], nil
		}
	}

	if subject == nil {
		subject = &asset.Subject{Slug: slug, DisplayName: displayName}
		_, err := s.repo.CreateSubject(ctx, subject)
		if err != nil {
			s.log.Warn("Ingest: subject might already exist", zap.String("slug", slug))
		}
	}

	key := "search:" + slug + ":" + lang
	result, err, _ := s.dedup.Do(key, func() (any, error) {
		return s.searchAndDownloadInner(ctx, slug, displayName, query, lang, tags, subject)
	})
	if err != nil {
		return nil, err
	}
	if asset, ok := result.(*asset.ImageAsset); ok {
		return asset, nil
	}
	return nil, fmt.Errorf("singleflight: unexpected result type")
}

func (s *ImageStorageService) searchAndDownloadInner(ctx context.Context, slug, displayName, query, lang string, tags []string, subject *asset.Subject) (*asset.ImageAsset, error) {
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
	imgURL, source, wikiURL := s.runRetrievalFallback(ctx, finalQuery, lang)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", finalQuery)
	}

	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	description := fmt.Sprintf("Image for %s found via %s", displayName, source)

	provCtx := context.WithValue(ctx, SourceTypeKey, "retrieved")
	provCtx = context.WithValue(provCtx, RetrieverKey, source)
	provCtx = context.WithValue(provCtx, SearchQueryKey, finalQuery)
	provCtx = context.WithValue(provCtx, ImageURLKey, imgURL)
	if wikiURL != "" {
		provCtx = context.WithValue(provCtx, PageURLKey, wikiURL)
	} else {
		provCtx = context.WithValue(provCtx, PageURLKey, imgURL)
	}
	if source == "wikipedia" {
		provCtx = context.WithValue(provCtx, LicenseKey, "CC-BY-SA-4.0")
		provCtx = context.WithValue(provCtx, AuthorKey, "Wikipedia Contributors")
	} else {
		provCtx = context.WithValue(provCtx, LicenseKey, "Unknown")
		provCtx = context.WithValue(provCtx, AuthorKey, "Unknown")
	}

	asset, err := s.downloadAndIngest(provCtx, slug, imgURL, slug, source, finalQuery, description, tags)
	if err == nil && asset != nil {
		meta := make(map[string]any)
		if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
			_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
		}
		meta["source_image_url"] = imgURL
		// runRetrievalFallback (Step 8) returns the third value as
		// the canonical page URL; we receive it via the local
		// `wikiURL` variable (preserved from the legacy cascade so
		// metadata writes don't churn).
		if wikiURL != "" {
			meta["source_page_url"] = wikiURL
		}
		meta["source_name"] = source
		meta["source_query"] = finalQuery
		metaJSON, marshalErr := json.Marshal(meta)
		if marshalErr != nil {
			s.log.Error("searchAndDownloadInner: failed to marshal metadata", zap.Error(marshalErr))
			return asset, fmt.Errorf("marshal image metadata: %w", marshalErr)
		}
		if updateErr := s.repo.UpdateImageMetadata(ctx, asset.Hash, string(metaJSON)); updateErr != nil {
			s.log.Error("searchAndDownloadInner: UpdateImageMetadata failed", zap.Error(updateErr))
			return asset, fmt.Errorf("update image metadata: %w", updateErr)
		}
		asset.MetadataJSON = string(metaJSON)
	}
	return asset, err
}

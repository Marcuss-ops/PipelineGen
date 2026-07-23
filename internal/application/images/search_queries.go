// Package images — search_queries.go is the canonical web image search
// entry point (SearchWebImage). Fan-out primitives → search_queries_fanout.go,
// per-engine backends → search_queries_engines.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3).
package images

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// SearchWebImage searches for a real image matching the prompt via DuckDuckGo
// and downloads+ingests it with retry on transient HTTP errors (429, 5xx).
//
// FASE 2.3 (July 2026): uses retry.Do via pkg/retry for transient errors;
// returns typed ErrImageNotFound on 404, ErrImageInvalidResponse on corrupt
// bodies, and ErrImageTransient on 429/5xx/timeout. Binary content passes
// via bytes.NewReader, not string(body).
func (s *ImageStorageService) SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*asset.ImageAsset, error) {
	if slug == "" {
		slug = textutil.Slugify(prompt)
	}
	s.log.Info("Searching web image", zap.String("prompt", prompt), zap.String("slug", slug))

	imgURL := s.searchDDGWide(ctx, prompt)
	if imgURL == "" {
		return nil, fmt.Errorf("no image found on DuckDuckGo for: %s", prompt)
	}
	s.log.Info("Found image URL on DuckDuckGo", zap.String("url", imgURL))

	// Check context cancellation before attempting download.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Download and ingest with retry on transient errors (429, 5xx, timeout).
	// Uses bytes.NewReader for binary content — no string(body) conversion.
	var imgAsset *asset.ImageAsset
	err := retry.Do(ctx, func() error {
		// Context check inside retry loop: abort immediately if cancelled.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		body, getErr := httpjson.GetBytes(ctx, s.client, imgURL, &httpjson.Options{
			UserAgent:    userAgent,
			MaxBodyBytes: 20 * 1024 * 1024,
		})
		if getErr != nil {
			var se *httpjson.StatusError
			if errors.As(getErr, &se) {
				switch {
				case se.StatusCode == http.StatusNotFound:
					return fmt.Errorf("%w: url=%s", ErrImageNotFound, imgURL)
				case se.StatusCode == http.StatusTooManyRequests || se.StatusCode >= 500:
					return fmt.Errorf("%w: HTTP %d", ErrImageTransient, se.StatusCode)
				default:
					return fmt.Errorf("%w: unexpected HTTP %d", ErrImageInvalidResponse, se.StatusCode)
				}
			}
			// Transport / ctx / timeout → transient (retryable).
			return fmt.Errorf("%w: %v", ErrImageTransient, getErr)
		}
		if len(body) == 0 {
			return fmt.Errorf("%w: downloaded image is empty", ErrImageInvalidResponse)
		}

		s.log.Info("Image downloaded", zap.Int("size_bytes", len(body)), zap.String("url", imgURL))

		filename := extractFilename(imgURL, prompt)
		description := fmt.Sprintf("Web image for: %s", prompt)

		// FASE 2.3: bytes.NewReader — nessuna conversione string(body).
		var ingestErr error
		imgAsset, ingestErr = s.IngestImage(ctx, slug, "", "", bytes.NewReader(body), filename, imgURL, description, tags, false, false)
		if ingestErr != nil {
			return fmt.Errorf("ingest image: %w", ingestErr)
		}
		return nil
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 500 * time.Millisecond,
		IsRetryable:    retry.IsTransient,
	})
	if err != nil {
		return nil, err
	}

	// Metadata enrichment — surface failure rather than silently ignoring it.
	updatedJSON := asset.AppendImageProvenance(imgAsset.MetadataJSON, imgURL, "", "duckduckgo", prompt)
	if updateErr := s.repo.UpdateImageMetadata(ctx, imgAsset.Hash, updatedJSON); updateErr != nil {
		s.log.Error("SearchWebImage: UpdateImageMetadata failed", zap.Error(updateErr))
		return imgAsset, fmt.Errorf("update image metadata: %w", updateErr)
	}
	imgAsset.MetadataJSON = updatedJSON

	s.log.Info("Web image ingested successfully",
		zap.String("slug", slug),
		zap.String("hash", imgAsset.Hash),
		zap.String("path", imgAsset.PathRel),
	)
	return imgAsset, nil
}

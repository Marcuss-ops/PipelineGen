// Package images — search_engine_commons.go contains the Wikimedia
// Commons image retrieval backend for the search_queries engines
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3; split 2026-08-07 to
// satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict).
//
// Owns: searchWikimediaCommons and the Commons REST types/helpers
// (commonsRESTSearchPayload, commonsRESTFilePayload, commonsImageInfo,
// waitForCommonsRequest, firstCommonsMetadata,
// commonsLicenseIsExplicit, stripHTMLMetadata).
package images

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/retrieved"
)

// searchWikimediaCommons is the explicit-license fallback for retrieval.
// Unlike a generic image search result, Commons imageinfo contains the
// license, author and dimensions needed by the VidRush rights gate.
func (s *ImageStorageService) searchWikimediaCommons(ctx context.Context, query string) retrieved.RetrievalSearchResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return retrieved.RetrievalSearchResult{}
	}
	if err := s.waitForCommonsRequest(ctx); err != nil {
		if s.log != nil {
			s.log.Warn("Wikimedia Commons request cancelled before rate-limit slot", zap.String("query", query), zap.Error(err))
		}
		return retrieved.RetrievalSearchResult{}
	}
	searchURL := "https://api.wikimedia.org/core/v1/commons/search/page?q=" + url.QueryEscape(query) + "&limit=8"
	commonsOptions := &httpjson.Options{
		UserAgent: userAgent,
		Headers:   map[string]string{"Api-User-Agent": userAgent},
	}
	var searchPayload commonsRESTSearchPayload
	err := retry.Do(ctx, func() error {
		var err error
		searchPayload, err = httpjson.GetJSON[commonsRESTSearchPayload](ctx, s.client, searchURL, commonsOptions)
		if err == nil {
			return nil
		}
		var statusErr *httpjson.StatusError
		if errors.As(err, &statusErr) && (statusErr.StatusCode == 429 || statusErr.StatusCode >= 500) {
			// Preserve both the provider-transient classification and the
			// typed HTTP envelope. The retry engine uses the latter to honor
			// Retry-After instead of retrying into the same 429 window.
			return fmt.Errorf("%w: commons HTTP %d: %w", ErrImageTransient, statusErr.StatusCode, err)
		}
		return err
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 350 * time.Millisecond,
		IsRetryable: func(err error) bool {
			// A 429 is provider backpressure, not a query failure. Do not
			// block the whole VidRush job behind a long Retry-After window;
			// SearchAll immediately continues with the next configured
			// retrieval provider. Retry transient 5xx/transport failures.
			var statusErr *httpjson.StatusError
			if errors.As(err, &statusErr) && statusErr.StatusCode == 429 {
				return false
			}
			return retry.IsTransient(err)
		},
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("Wikimedia Commons search failed", zap.String("query", query), zap.Error(err))
		}
		return retrieved.RetrievalSearchResult{}
	}
	for _, page := range searchPayload.Pages {
		if !strings.HasPrefix(page.Key, "File:") {
			continue
		}
		pageURL := "https://commons.wikimedia.org/wiki/" + url.PathEscape(page.Key)
		licensePayload, licenseErr := httpjson.GetJSON[commonsRESTPagePayload](ctx, s.client,
			"https://api.wikimedia.org/core/v1/commons/page/"+url.PathEscape(page.Key),
			commonsOptions)
		if licenseErr != nil || !commonsLicenseIsExplicit(licensePayload.License.Title) {
			continue
		}
		filePayload, fileErr := httpjson.GetJSON[commonsRESTFilePayload](ctx, s.client,
			"https://api.wikimedia.org/core/v1/commons/file/"+url.PathEscape(page.Key),
			commonsOptions)
		if fileErr != nil {
			continue
		}
		imageURL := firstNonEmptyImageURL(filePayload.Thumbnail.URL, filePayload.Preferred.URL, filePayload.Original.URL)
		if imageURL == "" || strings.HasSuffix(strings.ToLower(strings.Split(imageURL, "?")[0]), ".svg") {
			continue
		}
		width, height := filePayload.Thumbnail.Width, filePayload.Thumbnail.Height
		if width <= 0 || height <= 0 {
			width, height = filePayload.Original.Width, filePayload.Original.Height
		}
		return retrieved.RetrievalSearchResult{
			Provider: asset.ProviderWikimediaCommons, Origin: asset.ImageOriginRetrieved,
			PreviewURL: imageURL, PageURL: pageURL, Title: page.Title,
			Width: width, Height: height, License: licensePayload.License.Title,
			Author: filePayload.Latest.User.Name,
		}
	}
	if s.log != nil {
		s.log.Info("Wikimedia Commons returned no explicit-license image", zap.String("query", query))
	}
	return retrieved.RetrievalSearchResult{}
}

type commonsRESTSearchPayload struct {
	Pages []commonsRESTSearchPage `json:"pages"`
}

type commonsRESTSearchPage struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type commonsRESTPagePayload struct {
	License struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"license"`
}

type commonsRESTFilePayload struct {
	Latest struct {
		User struct {
			Name string `json:"name"`
		} `json:"user"`
	} `json:"latest"`
	Preferred commonsRESTImage `json:"preferred"`
	Original  commonsRESTImage `json:"original"`
	Thumbnail commonsRESTImage `json:"thumbnail"`
}

type commonsRESTImage struct {
	MIMEType string `json:"mediatype"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	URL      string `json:"url"`
}

const commonsMinimumRequestInterval = 2 * time.Second

func (s *ImageStorageService) waitForCommonsRequest(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.commonsSearchMu.Lock()
	defer s.commonsSearchMu.Unlock()
	if wait := commonsMinimumRequestInterval - time.Since(s.commonsLastSearch); wait > 0 {
		if err := retry.Sleep(ctx, wait, retry.Options{}); err != nil {
			return err
		}
	}
	s.commonsLastSearch = time.Now()
	return nil
}

func commonsLicenseIsExplicit(license string) bool {
	license = strings.ToLower(strings.TrimSpace(license))
	if license == "" || license == "unknown" || license == "n/a" || license == "copyrighted" {
		return false
	}
	for _, marker := range []string{"cc0", "cc by", "cc-by", "creative commons attribution", "public domain", "pd-", "free art license", "gfdl"} {
		if strings.Contains(license, marker) {
			return true
		}
	}
	return false
}

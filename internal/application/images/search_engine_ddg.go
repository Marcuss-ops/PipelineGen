// Package images — search_engine_ddg.go contains the DuckDuckGo image
// retrieval backend for the search_queries engines
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #3; split 2026-08-07 to
// satisfy the strict per-file LOC cap,
// architecture/policy.yaml#max_lines_per_file_strict).
//
// Owns: searchDDGWide, searchDDGWideMany, ddgImageResult, ddgImageScore.
package images

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/httpjson"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

func (s *ImageStorageService) searchDDGWide(ctx context.Context, query string) string {
	urls := s.searchDDGWideMany(ctx, query, 1)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

type ddgImageResult struct {
	Image     string `json:"image"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail"`
}

func (s *ImageStorageService) searchDDGWideMany(ctx context.Context, query string, limit int) []string {
	if limit <= 0 {
		limit = 10
	}
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s&iax=images&ia=images", url.QueryEscape(query))
	body, err := httpjson.GetBytes(ctx, s.client, vqdURL, &httpjson.Options{UserAgent: userAgent})
	if err != nil {
		s.log.Warn("DDG vqd extraction failed", zap.Error(err))
		return nil
	}
	vqd := extractVQD(string(body))
	if vqd == "" {
		return nil
	}
	all := make([]ddgImageResult, 0, limit)
	seen := make(map[string]struct{}, limit)
	nextURL := fmt.Sprintf("https://duckduckgo.com/i.js?l=en-us&o=json&q=%s&vqd=%s&f=,,,&p=0",
		url.QueryEscape(query), url.QueryEscape(vqd))
	seenPages := make(map[string]struct{}, 5)
	for len(all) < limit && nextURL != "" {
		if _, seenPage := seenPages[nextURL]; seenPage {
			break
		}
		seenPages[nextURL] = struct{}{}
		apiURL := nextURL

		// Retry the per-page HTTP call up to 3 times on transient errors.
		// httpjson.GetBytes wraps transport / status / decode errors;
		// ErrImageTransient shim makes retry.IsTransient recognise the
		// retryable subset byte-stable with the pre-B4 substring-match.
		var body []byte
		err := retry.Do(ctx, func() error {
			gotBody, getErr := httpjson.GetBytes(ctx, s.client, apiURL, &httpjson.Options{UserAgent: userAgent})
			if getErr != nil {
				return fmt.Errorf("%w: %v", ErrImageTransient, getErr)
			}
			body = gotBody
			return nil
		}, retry.Options{
			MaxAttempts:    3,
			InitialBackoff: 200 * time.Millisecond,
			IsRetryable:    retry.IsTransient,
		})
		if err != nil {
			// Preserve results already collected. A transient failure on a
			// later DDG page must not turn a valid first page into an empty
			// search result.
			break
		}
		var payload struct {
			Results []ddgImageResult `json:"results"`
			Next    string           `json:"next"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || len(payload.Results) == 0 {
			break
		}
		for _, result := range payload.Results {
			candidate := result.Image
			if candidate == "" {
				candidate = result.Thumbnail
			}
			if !strings.HasPrefix(candidate, "http") {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			all = append(all, result)
		}
		nextURL = ""
		if payload.Next != "" {
			nextURL = payload.Next
			if !strings.HasPrefix(nextURL, "http://") && !strings.HasPrefix(nextURL, "https://") {
				nextURL = "https://duckduckgo.com/" + strings.TrimPrefix(nextURL, "/")
			}
			// DDG's continuation URL intentionally omits vqd; retain the
			// session token required by /i.js when following it.
			if parsed, parseErr := url.Parse(nextURL); parseErr == nil && parsed.Query().Get("vqd") == "" {
				values := parsed.Query()
				values.Set("vqd", vqd)
				parsed.RawQuery = values.Encode()
				nextURL = parsed.String()
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return ddgImageScore(all[i]) > ddgImageScore(all[j])
	})
	out := make([]string, 0, min(limit, len(all)*2))
	seenOutput := make(map[string]struct{}, cap(out))
	for _, result := range all {
		// Prefer the original source image so the technical minimum
		// dimensions can be satisfied. Keep DuckDuckGo's real thumbnail as
		// fallback when an origin host rejects server-side acquisition.
		for _, candidate := range []string{result.Image, result.Thumbnail} {
			if !strings.HasPrefix(candidate, "http") {
				continue
			}
			if _, ok := seenOutput[candidate]; ok {
				continue
			}
			seenOutput[candidate] = struct{}{}
			out = append(out, candidate)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func ddgImageScore(r ddgImageResult) int {
	switch {
	case r.Width >= 1920 && r.Height >= 1080:
		return 100
	case r.Width >= 1280 && r.Height >= 720:
		return 70
	case r.Width >= 800:
		return 40
	default:
		return 10
	}
}

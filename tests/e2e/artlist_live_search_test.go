// Package e2e — Artlist live-search E2E test (ART-002 P2.1, July 2026).
//
// Self-contained end-to-end test for the live-search path:
// scraper.Provider (real, in internal/infrastructure/artlist/scraper)
// talking to a mock Node scraper server (httptest.NewServer returning
// the canonical Response JSON shape). Asserts the search round-trip
// returns the expected candidates, exercising the full HTTP
// roundtrip + JSON decode + adapter-to-port translation.
//
// Test isolation: each test uses a fresh httptest.NewServer (no
// shared state). The test does NOT require a real Node installation
// (no exec fallback is exercised) — see
// artlist_fallback_test.go (P2.3) for the fallback scenario.
//
// godlike/06 SSOT: the test exercises the canonical production
// scraper.Provider (not a stub) so any signature drift in the
// Searcher port surfaces as a build failure (the Provider is
// compile-time-pinned via `var _ artapp.Searcher = (*Provider)(nil)`
// in the production code).
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/scraper"
)

// TestE2E_Artlist_LiveSearch_NodeOn exercises the live search path
// with a real scraper.Provider talking to a mock Node scraper
// server. Covers 4 sub-scenarios that collectively pin the
// scraper's response contract (per scraper.go:122-138):
//   - happy_path_2_candidates: 200 OK + 2 clips → 2 Candidates
//   - 4xx_invalid_response: 404 → artapp.ErrInvalidResponse
//   - ok_false_empty_result: 200 OK + ok=false (no clips) → artapp.ErrEmptyResult
//   - empty_clips_empty_result: 200 OK + clips=[] → artapp.ErrEmptyResult
//
// The 5xx → artapp.ErrTransportFallback → exec fallback path is
// covered separately in artlist_fallback_test.go (P2.3) so this
// file can focus on the Node-server response contract without
// requiring `node` in PATH (the exec fallback is environment-
// dependent and out of scope for the live-search happy/error
// taxonomy).
//
// godlike/07 no-fake-availability: each subtest exercises a real
// scraper.Provider response branch (not a stubbed Search return).
// godlike/06 SSOT: the production scraper.Provider is the
// canonical concrete satisfying artapp.Searcher
// (var _ artapp.Searcher = (*Provider)(nil) in scraper.go).
func TestE2E_Artlist_LiveSearch_NodeOn(t *testing.T) {
	t.Run("happy_path_2_candidates", func(t *testing.T) {
		// Mock Node scraper: returns 2 candidates per the Response struct shape.
		var receivedBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/v1/clips/search", r.URL.Path, "scraper must POST to /v1/clips/search")
			require.Equal(t, http.MethodPost, r.Method, "scraper must use POST")
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":         true,
				"term":       "test",
				"search_url": "https://artlist.com/search/test",
				"clips": []map[string]any{
					{
						"clip_id":       "clip-1",
						"id":            "1",
						"title":         "Test Clip 1",
						"name":          "Test Clip 1",
						"primary_url":   "https://cdn.example.com/clip-1.mp4",
						"stream_urls":   []string{"https://cdn.example.com/clip-1.m3u8"},
						"clip_page_url": "https://artlist.com/clip/1",
					},
					{
						"clip_id":       "clip-2",
						"id":            "2",
						"title":         "Test Clip 2",
						"name":          "Test Clip 2",
						"primary_url":   "https://cdn.example.com/clip-2.mp4",
						"clip_page_url": "https://artlist.com/clip/2",
					},
				},
			})
		}))
		defer srv.Close()

		provider := scraper.New(scraper.Config{
			ServerURL:   srv.URL,
			HTTPTimeout: 5 * time.Second,
		}, zap.NewNop())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		candidates, err := provider.Search(ctx, artapp.SearchRequest{Term: "test", Limit: 8})
		require.NoError(t, err)
		require.Len(t, candidates, 2)

		// Assert received body (proves the SearchRequest was correctly serialized).
		require.Equal(t, "test", receivedBody["term"])
		require.Equal(t, float64(8), receivedBody["limit"]) // JSON numbers decode to float64

		// Assert candidate #1.
		require.Equal(t, "clip-1", candidates[0].ID)
		require.Equal(t, "Test Clip 1", candidates[0].Title)
		require.Equal(t, "https://cdn.example.com/clip-1.mp4", candidates[0].SourceRef)
		require.Equal(t, "https://artlist.com/clip/1", candidates[0].PageURL)

		// Assert candidate #2.
		require.Equal(t, "clip-2", candidates[1].ID)
		require.Equal(t, "Test Clip 2", candidates[1].Title)
		require.Equal(t, "https://cdn.example.com/clip-2.mp4", candidates[1].SourceRef)
	})

	t.Run("4xx_invalid_response", func(t *testing.T) {
		// Per scraper.go:128-130: status != 200 (and not 5xx) → ErrInvalidResponse.
		// The scraper does NOT fall back to exec on 4xx (per the explicit
		// "do NOT fall back" comment at scraper.go:114), so the error
		// is propagated verbatim.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer srv.Close()

		provider := scraper.New(scraper.Config{
			ServerURL:   srv.URL,
			HTTPTimeout: 5 * time.Second,
		}, zap.NewNop())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := provider.Search(ctx, artapp.SearchRequest{Term: "test", Limit: 8})
		require.Error(t, err)
		require.ErrorIs(t, err, artapp.ErrInvalidResponse,
			"4xx non-200 must propagate as ErrInvalidResponse (not fall back to exec)")
	})

	t.Run("ok_false_empty_result", func(t *testing.T) {
		// Per scraper.go:140-144: 200 with ok=false → returns the response
		// (no error). Then Search at scraper.go:121 checks len(Clips) == 0
		// and returns ErrEmptyResult. This is the "Node server reached
		// but no clips matched" path.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"term":  "test",
				"clips": []any{},
			})
		}))
		defer srv.Close()

		provider := scraper.New(scraper.Config{
			ServerURL:   srv.URL,
			HTTPTimeout: 5 * time.Second,
		}, zap.NewNop())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		candidates, err := provider.Search(ctx, artapp.SearchRequest{Term: "test", Limit: 8})
		require.Nil(t, candidates, "ok=false must produce nil candidates (the response was empty)")
		require.ErrorIs(t, err, artapp.ErrEmptyResult,
			"200 + ok=false must propagate as ErrEmptyResult (the Node server said no clips matched)")
	})

	t.Run("empty_clips_empty_result", func(t *testing.T) {
		// Per scraper.go:140-144: 200 with ok=true but clips=[] →
		// ErrEmptyResult (same as ok=false path).
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    true,
				"term":  "test",
				"clips": []any{},
			})
		}))
		defer srv.Close()

		provider := scraper.New(scraper.Config{
			ServerURL:   srv.URL,
			HTTPTimeout: 5 * time.Second,
		}, zap.NewNop())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		candidates, err := provider.Search(ctx, artapp.SearchRequest{Term: "test", Limit: 8})
		require.Nil(t, candidates)
		require.ErrorIs(t, err, artapp.ErrEmptyResult,
			"200 + clips=[] must propagate as ErrEmptyResult")
	})
}

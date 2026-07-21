// Package pexels — provider_test.go pins Fase 4.1 canonical surfaces.
//
//  1. Image provider canonical projection: Pexels HTTP /v1/images/search
//     → providers.Candidate[] with MediaType=image and SourceName stamped.
//  2. Typed-sentinel envelopes: empty API key / empty term / non-200
//     HTTP status all surface canonical ErrUnavailable or
//     ErrInvalidResponse (no wire-shape leak).
//  3. Capabilities() advertises search + image so the canonical
//     SearchFanOut aggregator routes image queries here.
package pexels

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// makePexelsServer returns an httptest server that mirrors the
// canonical /v1/images/search envelope shape (photos[]).
func makePexelsServer(t *testing.T, photos any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/images/search") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, `{"error":"missing api key"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"photos":        photos,
			"total_results": 2,
			"page":          1,
			"per_page":      2,
		})
	}))
	return srv
}

// Test 1: Capabilities advertise search + image.
func TestProvider_CapabilitiesAdvertiseImage(t *testing.T) {
	p := NewProvider(Config{APIKey: "x"})
	caps := p.Capabilities()
	hasSearch, hasImage := false, false
	for _, c := range caps {
		if c == providers.CapabilitySearch {
			hasSearch = true
		}
		if c == providers.CapabilityImage {
			hasImage = true
		}
	}
	if !hasSearch {
		t.Fatal("expected CapabilitySearch")
	}
	if !hasImage {
		t.Fatal("expected CapabilityImage")
	}
}

// Test 2: Name = canonical ProviderName constant.
func TestProvider_NameIsCanonical(t *testing.T) {
	p := NewProvider(Config{APIKey: "x"})
	if p.Name() != ProviderName {
		t.Fatalf("Name()=%q, want %q", p.Name(), ProviderName)
	}
	if p.SourceName() != ProviderName {
		t.Fatalf("SourceName()=%q, want %q", p.SourceName(), ProviderName)
	}
}

// Test 3: empty API key → ErrUnavailable (godlike/07).
func TestProvider_EmptyAPIKeyReturnsErrUnavailable(t *testing.T) {
	p := NewProvider(Config{})
	_, err := p.Search(context.Background(), providers.SearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
	if !errors.Is(err, artapp.ErrUnavailable) {
		t.Fatalf("expected wrapped ErrUnavailable, got %v", err)
	}
}

// Test 4: empty term → ErrEmpty.
func TestProvider_EmptyTermReturnsErrEmpty(t *testing.T) {
	p := NewProvider(Config{APIKey: "x"})
	_, err := p.Search(context.Background(), providers.SearchRequest{Query: "   "})
	if err == nil {
		t.Fatal("expected error for empty term")
	}
	if !errors.Is(err, artapp.ErrEmpty) {
		t.Fatalf("expected wrapped ErrEmpty, got %v", err)
	}
}

// Test 5: happy-path HTTP returns canonical projection.
func TestProvider_HappyPathProjectsImageCandidates(t *testing.T) {
	photos := []map[string]any{
		{
			"id": 100, "width": 1920, "height": 1080,
			"url":          "https://pexels.com/photo/100",
			"photographer": "Alice",
			"src": map[string]any{
				"original": "https://images.pexels.com/100/original.jpg",
				"large":    "https://images.pexels.com/100/large.jpg",
				"tiny":     "https://images.pexels.com/100/tiny.jpg",
			},
		},
		{
			"id": 200, "width": 800, "height": 600,
			"url":          "https://pexels.com/photo/200",
			"photographer": "Bob",
			"src": map[string]any{
				"original":  "https://images.pexels.com/200/original.jpg",
				"landscape": "https://images.pexels.com/200/landscape.jpg",
			},
		},
	}
	srv := makePexelsServer(t, photos)
	defer srv.Close()

	p := NewProvider(Config{APIKey: "test-key", BaseURL: srv.URL})
	res, err := p.Search(context.Background(), providers.SearchRequest{Query: "Maya temple", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(res.Candidates))
	}
	c0 := res.Candidates[0]
	if c0.MediaType != asset.MediaTypeImage {
		t.Fatalf("candidate[0].MediaType=%q, want %q", c0.MediaType, asset.MediaTypeImage)
	}
	if c0.Provider != ProviderName {
		t.Fatalf("candidate[0].Provider=%q, want %q", c0.Provider, ProviderName)
	}
	if c0.PreviewURL != "https://images.pexels.com/100/large.jpg" {
		t.Fatalf("candidate[0].PreviewURL=%q, want large.jpg URL", c0.PreviewURL)
	}
	if c0.Width != 1920 || c0.Height != 1080 {
		t.Fatalf("candidate[0] dims=%dx%d, want 1920x1080", c0.Width, c0.Height)
	}
	if c0.Orientation != "landscape" {
		t.Fatalf("candidate[0].Orientation=%q, want landscape", c0.Orientation)
	}
	c1 := res.Candidates[1]
	if c1.Width != 800 || c1.Height != 600 {
		t.Fatalf("candidate[1] dims=%dx%d, want 800x600", c1.Width, c1.Height)
	}
	if res.NextPageToken != "" {
		t.Fatalf("expected empty NextPageToken, got %q", res.NextPageToken)
	}
}

// Test 6: Pexels 429 → ErrUnavailable (rate-limited).
func TestProvider_HTTP429ReturnsErrUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := NewProvider(Config{APIKey: "test-key", BaseURL: srv.URL})
	_, err := p.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, artapp.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for 429, got %v", err)
	}
}

// Test 7: Pexels 401 → ErrUnavailable (auth).
func TestProvider_HTTP401ReturnsErrUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := NewProvider(Config{APIKey: "bad-key", BaseURL: srv.URL})
	_, err := p.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, artapp.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for 401, got %v", err)
	}
}

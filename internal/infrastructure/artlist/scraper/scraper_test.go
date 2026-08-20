package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"go.uber.org/zap"
)

func TestNormalizeGatewayResponseUsesStableEnvelopeFields(t *testing.T) {
	resp := &Response{
		OK:       true,
		Provider: "artlist",
		Query:    "business team office",
		CacheHit: true,
		Results: []Clip{{
			ClipID:      "clip-1",
			Title:       "Business Team",
			ClipPageURL: "https://artlist.io/stock-footage/clip/clip-1",
			PrimaryURL:  "https://cdn/artlist/clip-1.mp4",
		}},
	}

	if err := normalizeGatewayResponse(resp, "ignored fallback"); err != nil {
		t.Fatalf("normalizeGatewayResponse returned error: %v", err)
	}

	if resp.Term != "business team office" {
		t.Fatalf("expected term to mirror query, got %q", resp.Term)
	}
	if resp.SearchURL != "https://artlist.io/stock-footage/search?terms=business+team+office" {
		t.Fatalf("unexpected fallback search_url: %q", resp.SearchURL)
	}
	if resp.Source != "sqlite" {
		t.Fatalf("expected cache-hit source to normalize to sqlite, got %q", resp.Source)
	}
	if len(resp.Clips) != 1 {
		t.Fatalf("expected results to be promoted into clips, got %d", len(resp.Clips))
	}
}

func TestNormalizeGatewayResponseRejectsUnexpectedProvider(t *testing.T) {
	resp := &Response{OK: true, Provider: "other"}
	err := normalizeGatewayResponse(resp, "term")
	if err == nil {
		t.Fatal("expected provider mismatch to fail")
	}
	if !strings.Contains(err.Error(), "unexpected provider") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchViaServerConsumesStableEnvelope(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/v1/clips/search" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if payload["query"] != "business team office" {
			t.Fatalf("unexpected query payload: %#v", payload["query"])
		}
		if payload["force_refresh"] != false {
			t.Fatalf("expected force_refresh false, got %#v", payload["force_refresh"])
		}
		if payload["mode"] != string(artapp.SearchModeCatalogFirst) {
			t.Fatalf("expected explicit catalog_first mode, got %#v", payload["mode"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"provider": "artlist",
			"query": "business team office",
			"cache_hit": true,
			"source": "sqlite",
			"results": [
				{
					"clip_id": "clip-1",
					"title": "Business Team",
					"clip_page_url": "https://artlist.io/stock-footage/clip/clip-1",
					"preview_url": "https://cdn/artlist/clip-1.mp4"
				}
			]
		}`))
	}))
	defer server.Close()

	provider := New(Config{
		ServerURL:   server.URL,
		HTTPTimeout: 5 * time.Second,
	}, zap.NewNop())

	clips, err := provider.Search(context.Background(), artapp.SearchRequest{
		Term:  "business team office",
		Limit: 8,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(clips) != 1 {
		t.Fatalf("expected one clip, got %d", len(clips))
	}

	clip := clips[0]
	if clip.ID != "clip-1" {
		t.Fatalf("unexpected clip id %q", clip.ID)
	}
	if clip.SourceName != "artlist" {
		t.Fatalf("unexpected source name %q", clip.SourceName)
	}
	if clip.SourceRef != "https://cdn/artlist/clip-1.mp4" {
		t.Fatalf("unexpected source ref %q", clip.SourceRef)
	}
}

func TestSearchViaServerForwardsForceRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if payload["force_refresh"] != true {
			t.Fatalf("expected force_refresh true, got %#v", payload["force_refresh"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"provider":"artlist","query":"fresh query","clips":[{"clip_id":"fresh-1","primary_url":"https://cdn.example/fresh-1.m3u8","clip_page_url":"https://artlist.io/clip/fresh-1"}]}`))
	}))
	defer server.Close()

	provider := New(Config{ServerURL: server.URL, HTTPTimeout: time.Second}, zap.NewNop())
	clips, err := provider.Search(context.Background(), artapp.SearchRequest{Term: "fresh query", Limit: 1, ForceRefresh: true})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(clips) != 1 || clips[0].ID != "fresh-1" {
		t.Fatalf("unexpected clips: %+v", clips)
	}
}

func TestSearchViaServerForwardsExplicitModes(t *testing.T) {
	modes := []artapp.SearchMode{
		artapp.SearchModeCatalogFirst,
		artapp.SearchModeCatalogOnly,
		artapp.SearchModeLiveRequired,
	}
	for _, wantMode := range modes {
		t.Run(string(wantMode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("failed to decode request: %v", err)
				}
				if payload["mode"] != string(wantMode) {
					t.Fatalf("mode payload = %#v, want %q", payload["mode"], wantMode)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"provider":"artlist","query":"mode test","clips":[{"clip_id":"mode-1","clip_page_url":"https://artlist.io/clip/mode-1"}]}`))
			}))
			defer server.Close()

			provider := New(Config{ServerURL: server.URL, HTTPTimeout: time.Second}, zap.NewNop())
			clips, err := provider.Search(context.Background(), artapp.SearchRequest{
				Term: "mode test", Limit: 1, Mode: wantMode,
			})
			if err != nil {
				t.Fatalf("Search returned error: %v", err)
			}
			if len(clips) != 1 || clips[0].ID != "mode-1" {
				t.Fatalf("unexpected clips: %+v", clips)
			}
		})
	}
}

func TestSearchSerializesBrowserRequests(t *testing.T) {
	var active, maxActive int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&active, 1)
		for {
			previous := atomic.LoadInt32(&maxActive)
			if current <= previous || atomic.CompareAndSwapInt32(&maxActive, previous, current) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"provider":"artlist","query":"mountain sunrise","clips":[]}`))
	}))
	defer server.Close()

	provider := New(Config{ServerURL: server.URL, HTTPTimeout: time.Second}, zap.NewNop())
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = provider.Search(context.Background(), artapp.SearchRequest{Term: "mountain sunrise", Limit: 1})
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("concurrent Artlist searches = %d, want provider gate to keep it at 1", got)
	}
}

func TestSearchDoesNotFallbackToExecWhenServerRateLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error":"ARTLIST_RATE_LIMITED"}`))
	}))
	defer server.Close()

	provider := New(Config{ServerURL: server.URL, HTTPTimeout: time.Second}, zap.NewNop())
	_, err := provider.Search(context.Background(), artapp.SearchRequest{Term: "mountain sunrise", Limit: 1})
	if !errors.Is(err, artapp.ErrRateLimited) {
		t.Fatalf("Search error = %v, want ErrRateLimited", err)
	}
	if errors.Is(err, artapp.ErrTransportFallback) {
		t.Fatalf("rate-limited search must not trigger exec fallback: %v", err)
	}
}

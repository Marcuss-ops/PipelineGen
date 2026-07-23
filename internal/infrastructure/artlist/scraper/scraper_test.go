package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

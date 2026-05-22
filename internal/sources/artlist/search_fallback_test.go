package artlist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"velox/go-master/internal/config"
)

func TestSearchPixabayVideosUsesConfiguredBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/videos/" {
			t.Fatalf("unexpected path: %s", got)
		}
		if got := r.URL.Query().Get("key"); got != "pixabay-key" {
			t.Fatalf("unexpected key: %s", got)
		}
		if got := r.URL.Query().Get("q"); got != "forest trail" {
			t.Fatalf("unexpected query: %s", got)
		}
		if got := r.URL.Query().Get("per_page"); got != "3" {
			t.Fatalf("unexpected per_page: %s", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": [
				{
					"id": 77,
					"pageURL": "https://pixabay.com/videos/77/",
					"tags": "forest, trail",
					"videos": {
						"medium": {"url": "https://cdn.pixabay.test/77-medium.mp4"},
						"large": {"url": "https://cdn.pixabay.test/77-large.mp4"}
					}
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		External: config.ExternalConfig{
			PixabayAPIKey:  "pixabay-key",
			PixabayBaseURL: server.URL,
		},
	}
	ss := &SearchService{service: &Service{cfg: cfg, log: zap.NewNop()}}

	clips, err := ss.searchPixabayVideos(context.Background(), "forest trail", 3)
	if err != nil {
		t.Fatalf("searchPixabayVideos returned error: %v", err)
	}
	if len(clips) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(clips))
	}
	if clips[0].ClipID != "pixabay-77" {
		t.Fatalf("unexpected clip id: %s", clips[0].ClipID)
	}
	if clips[0].PrimaryURL != "https://cdn.pixabay.test/77-medium.mp4" {
		t.Fatalf("unexpected primary url: %s", clips[0].PrimaryURL)
	}
	if !strings.Contains(clips[0].Title, "Pixabay:") {
		t.Fatalf("expected pixabay title, got %q", clips[0].Title)
	}
}

func TestSearchPexelsVideosUsesConfiguredBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/videos/search" {
			t.Fatalf("unexpected path: %s", got)
		}
		if got := r.URL.Query().Get("query"); got != "city night" {
			t.Fatalf("unexpected query: %s", got)
		}
		if got := r.URL.Query().Get("per_page"); got != "2" {
			t.Fatalf("unexpected per_page: %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "pexels-key" {
			t.Fatalf("unexpected auth header: %s", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"videos": [
				{
					"id": 991,
					"url": "https://www.pexels.com/video/991/",
					"user": {"name": "Alex Doe", "url": "https://www.pexels.com/@alex"},
					"video_files": [
						{"id": 1, "quality": "sd", "file_type": "mp4", "width": 640, "height": 360, "link": "https://cdn.pexels.test/991-sd.mp4"},
						{"id": 2, "quality": "hd", "file_type": "mp4", "width": 1920, "height": 1080, "link": "https://cdn.pexels.test/991-hd.mp4"}
					]
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		External: config.ExternalConfig{
			PexelsAPIKey:  "pexels-key",
			PexelsBaseURL: server.URL,
		},
	}
	ss := &SearchService{service: &Service{cfg: cfg, log: zap.NewNop()}}

	clips, err := ss.searchPexelsVideos(context.Background(), "city night", 2)
	if err != nil {
		t.Fatalf("searchPexelsVideos returned error: %v", err)
	}
	if len(clips) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(clips))
	}
	if clips[0].ClipID != "pexels-991" {
		t.Fatalf("unexpected clip id: %s", clips[0].ClipID)
	}
	if clips[0].PrimaryURL != "https://cdn.pexels.test/991-hd.mp4" {
		t.Fatalf("unexpected primary url: %s", clips[0].PrimaryURL)
	}
	if !strings.Contains(clips[0].Title, "Pexels:") {
		t.Fatalf("expected pexels title, got %q", clips[0].Title)
	}
}

func TestSearchLiveFallsBackToPixabayWhenArtlistUnavailable(t *testing.T) {
	t.Setenv("VELOX_NODE_SCRAPER_DIR", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/videos/" {
			t.Fatalf("unexpected path: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": [
				{
					"id": 501,
					"pageURL": "https://pixabay.com/videos/501/",
					"tags": "waterfall, river",
					"videos": {"medium": {"url": "https://cdn.pixabay.test/501.mp4"}}
				}
			]
		}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		External: config.ExternalConfig{
			PixabayAPIKey:  "pixabay-key",
			PixabayBaseURL: server.URL,
		},
	}
	ss := &SearchService{service: &Service{cfg: cfg, log: zap.NewNop()}}

	clips, err := ss.SearchLive(context.Background(), "waterfall", 1)
	if err != nil {
		t.Fatalf("SearchLive returned error: %v", err)
	}
	if len(clips) != 1 {
		t.Fatalf("expected 1 clip, got %d", len(clips))
	}
	if clips[0].ClipID != "pixabay-501" {
		t.Fatalf("unexpected clip id: %s", clips[0].ClipID)
	}
}

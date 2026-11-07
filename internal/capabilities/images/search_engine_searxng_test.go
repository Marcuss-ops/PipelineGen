package images

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestSearXNGImageSearchRequestsRecentWindowForCurrentTopics(t *testing.T) {
	var searchQuery string
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `OK`
		if req.URL.Path == "/search" {
			searchQuery = req.URL.RawQuery
			body = `{"results":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	cfg := &config.Config{}
	cfg.External.SearxngURL = "https://searx.example"
	service := &ImageStorageService{cfg: cfg, client: client, log: zap.NewNop()}
	service.searchSearXNGImagesMany(context.Background(), "Artemis II "+time.Now().UTC().Format("2006"), 5)
	if !strings.Contains(searchQuery, "time_range=year") {
		t.Fatalf("current-topic query params = %q, want time_range=year", searchQuery)
	}
}

func TestSearXNGImageSearchDoesNotConstrainEvergreenTopics(t *testing.T) {
	var searchQuery string
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `OK`
		if req.URL.Path == "/search" {
			searchQuery = req.URL.RawQuery
			body = `{"results":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	cfg := &config.Config{}
	cfg.External.SearxngURL = "https://searx.example"
	service := &ImageStorageService{cfg: cfg, client: client, log: zap.NewNop()}
	service.searchSearXNGImagesMany(context.Background(), "White House architecture", 5)
	if strings.Contains(searchQuery, "time_range=") {
		t.Fatalf("evergreen query params = %q, want no recency filter", searchQuery)
	}
}

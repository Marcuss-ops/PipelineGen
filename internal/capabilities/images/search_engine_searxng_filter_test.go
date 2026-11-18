package images

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestSearxImageSourceDeniedMatchesHostAndSubdomains(t *testing.T) {
	denied := []string{
		"https://it.pinterest.com/pin/1",
		"https://i.pinimg.com/originals/aa.jpg",
		"https://www.artic.edu/artworks/12665",
		"https://www.gettyimages.com/detail/x",
		"https://stock.adobe.com/images/x",
		"https://wallpapers.com/wallpapers/x.html",
		"https://www.alamy.com/stock-photo-x.html",
	}
	for _, raw := range denied {
		if !searxImageSourceDenied(raw) {
			t.Errorf("searxImageSourceDenied(%q) = false, want true", raw)
		}
	}
	allowed := []string{
		"https://commons.wikimedia.org/wiki/File:X.jpg",
		"https://upload.wikimedia.org/wikipedia/commons/a/aa/X.jpg",
		"https://www.nytimes.com/2020/01/01/x.html",
		"https://www.imdb.com/name/nm0000206/",
		"https://notpinterest.com/x.jpg",
		"",
	}
	for _, raw := range allowed {
		if searxImageSourceDenied(raw) {
			t.Errorf("searxImageSourceDenied(%q) = true, want false", raw)
		}
	}
}

func TestSearXNGImageSearchDropsDeniedSourcesWithoutSpendingTheBudget(t *testing.T) {
	payload := `{"results":[
		{"url":"https://it.pinterest.com/pin/1","img_src":"https://i.pinimg.com/originals/1.jpg"},
		{"url":"https://www.gettyimages.com/detail/x","img_src":"https://media.gettyimages.com/x.jpg"},
		{"url":"https://commons.wikimedia.org/wiki/File:Subject.jpg","img_src":"https://upload.wikimedia.org/wikipedia/commons/a/aa/Subject.jpg"},
		{"url":"https://www.alamy.com/stock-photo-x.html","img_src":"https://c8.alamy.com/x.jpg"},
		{"url":"https://www.nytimes.com/2020/01/01/subject.html","img_src":"https://static01.nyt.com/subject.jpg"},
		{"url":"https://www.pexels.com/photo/x","img_src":"https://images.pexels.com/photos/1/x.jpeg"}
	]}`
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := "OK"
		if req.URL.Path == "/search" {
			body = payload
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	cfg := &config.Config{}
	cfg.External.SearxngURL = "https://searx.example"
	service := &ImageStorageService{cfg: cfg, client: client, log: zap.NewNop()}

	// Limit 2 with four denied sources ahead: an implementation that counted
	// denied rows against the budget would return nothing.
	got := service.searchSearXNGImagesMany(context.Background(), "Chiara Poggi", 2)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want the 2 allowed sources", len(got))
	}
	for _, candidate := range got {
		if searxImageSourceDenied(candidate.PreviewURL) || searxImageSourceDenied(candidate.PageURL) {
			t.Fatalf("denied source leaked into results: %+v", candidate)
		}
	}
	if got[0].PreviewURL != "https://upload.wikimedia.org/wikipedia/commons/a/aa/Subject.jpg" {
		t.Fatalf("first allowed candidate = %q", got[0].PreviewURL)
	}
}

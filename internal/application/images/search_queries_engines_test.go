package images

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type commonsRESTRoundTripper func(*http.Request) (*http.Response, error)

func (f commonsRESTRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSearchWikimediaCommons_UsesRESTLicenseAndFileMetadata(t *testing.T) {
	client := &http.Client{Transport: commonsRESTRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Api-User-Agent") == "" {
			t.Fatalf("Wikimedia request missing identifying headers: user-agent=%q api-user-agent=%q", r.Header.Get("User-Agent"), r.Header.Get("Api-User-Agent"))
		}
		var body string
		switch {
		case strings.Contains(r.URL.Path, "/search/page"):
			body = `{"pages":[{"key":"File:Example.jpg","title":"File:Example.jpg"}]}`
		case strings.Contains(r.URL.Path, "/page/File:Example.jpg"):
			body = `{"license":{"title":"Creative Commons Attribution-ShareAlike 4.0","url":"https://creativecommons.org/licenses/by-sa/4.0/"}}`
		case strings.Contains(r.URL.Path, "/file/File:Example.jpg"):
			body = `{"latest":{"user":{"name":"Example Author"}},"thumbnail":{"mediatype":"BITMAP","width":1920,"height":1080,"url":"https://upload.wikimedia.org/example/1920px-Example.jpg"},"original":{"width":4000,"height":2250,"url":"https://upload.wikimedia.org/example/Example.jpg"}}`
		default:
			t.Fatalf("unexpected REST request: %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	service := &ImageStorageService{client: client, log: zap.NewNop()}
	got := service.searchWikimediaCommons(context.Background(), "motorcycle")
	if got.Provider != "wikimedia_commons" || got.License == "" || got.Author != "Example Author" {
		t.Fatalf("unexpected Commons result: %+v", got)
	}
	if got.PreviewURL == "" || got.Width != 1920 || got.Height != 1080 {
		t.Fatalf("missing REST file metadata: %+v", got)
	}
}

package workflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestSearchDDGWideUsesVQDAndImagesEndpoint(t *testing.T) {
	var requests []*http.Request
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Clone(req.Context()))
		body := ""
		if req.URL.Path == "/" {
			body = `<html><script>vqd="12345-678"</script></html>`
		} else if req.URL.Path == "/i.js" {
			payload := struct {
				Results []map[string]any `json:"results"`
			}{Results: []map[string]any{{
				"image":  "https://images.example/red-fox.jpg",
				"width":  1920,
				"height": 1080,
			}}}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal DDG fixture: %v", err)
			}
			body = string(encoded)
		} else {
			t.Fatalf("unexpected DDG path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	service := &ImageStorageService{client: client, log: zap.NewNop()}

	got := service.searchDDGWide(context.Background(), "red fox in snow")
	if got != "https://images.example/red-fox.jpg" {
		t.Fatalf("searchDDGWide() = %q, want fixture image URL", got)
	}
	if len(requests) != 2 {
		t.Fatalf("DDG requests = %d, want homepage + /i.js", len(requests))
	}
	if requests[1].URL.Path != "/i.js" {
		t.Fatalf("second DDG path = %q, want /i.js", requests[1].URL.Path)
	}
	values := requests[1].URL.Query()
	if values.Get("vqd") != "12345-678" || values.Get("q") != "red fox in snow" {
		t.Fatalf("/i.js query = %v, missing q/vqd", values)
	}
	if _, err := url.Parse(got); err != nil {
		t.Fatalf("returned URL is invalid: %v", err)
	}
}

func TestSearchDDGWideInvalidVQDResponseReturnsEmpty(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("<html>no token</html>")),
			Request:    req,
		}, nil
	})}
	service := &ImageStorageService{client: client, log: zap.NewNop()}
	if got := service.searchDDGWide(context.Background(), "broken response"); got != "" {
		t.Fatalf("searchDDGWide() = %q, want empty result", got)
	}
}

func TestSearchDDGWideManyFollowsContinuationURL(t *testing.T) {
	var pages []string
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/" {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`<script>vqd="12345-678"</script>`)), Request: req}, nil
		}
		if req.URL.Path != "/i.js" {
			t.Fatalf("unexpected DDG path %q", req.URL.Path)
		}
		pages = append(pages, req.URL.RawQuery)
		payload := `{"results":[{"image":"https://images.example/page-1.jpg","width":1920,"height":1080}],"next":"i.js?q=red+fox&o=json&p=1&s=100&u=bing&f=,,,&l=en-us"}`
		if req.URL.Query().Get("s") == "100" {
			payload = `{"results":[{"image":"https://images.example/page-2.jpg","width":1920,"height":1080}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
	})}
	service := &ImageStorageService{client: client, log: zap.NewNop()}
	got := service.searchDDGWideMany(context.Background(), "red fox", 2)
	if len(got) != 2 || got[0] != "https://images.example/page-1.jpg" || got[1] != "https://images.example/page-2.jpg" {
		t.Fatalf("continuation results = %#v, want two distinct pages", got)
	}
	if len(pages) != 2 || !strings.Contains(pages[1], "s=100") || !strings.Contains(pages[1], "vqd=12345-678") {
		t.Fatalf("continuation query = %#v, want s=100 and vqd", pages)
	}
}

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

package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebSearcherSearXNGJSONAndLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" || r.URL.Query().Get("categories") != "general" || r.URL.Query().Get("language") != "en" || r.URL.Query().Get("safesearch") != "0" {
			t.Fatalf("bad request: %s", r.URL.String())
		}
		if r.Header.Get("Accept") != "application/json" || !strings.Contains(r.Header.Get("User-Agent"), "PipelineGen") {
			t.Fatalf("missing diagnostic headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"one","content":"a","url":"https://example.com/1"},{"title":"two","content":"b","url":"https://example.com/2"}]}`))
	}))
	defer server.Close()
	got, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "boxer")
	if err != nil || len(got) != 2 || got[0].URL != "https://example.com/1" {
		t.Fatalf("results=%#v err=%v", got, err)
	}
}

func TestWebSearcherSendsConfiguredEnginesAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("engines") != "bing,mwmbl" {
			t.Fatalf("engines=%q", r.URL.Query().Get("engines"))
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"one","content":"a","url":"https://example.com/1"}]}`))
	}))
	defer server.Close()
	got, err := NewWebSearcherWithConfig(WebSearcherConfig{BaseURL: server.URL, MaxResults: 1, Timeout: time.Second, Engines: []string{"mwmbl", "bing", "bing"}}).Search(context.Background(), "boxer")
	if err != nil || len(got) != 1 {
		t.Fatalf("results=%#v err=%v", got, err)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer slow.Close()
	_, err = NewWebSearcherWithConfig(WebSearcherConfig{BaseURL: slow.URL, Timeout: 10 * time.Millisecond}).Search(context.Background(), "slow")
	var searchErr *SearchError
	if !errors.As(err, &searchErr) || searchErr.Code != SearchErrorUnreachable {
		t.Fatalf("timeout error=%v", err)
	}
}

func TestWebSearcherRejectsBadJSONAndHTTPError(t *testing.T) {
	for _, test := range []struct {
		status int
		code   SearchErrorCode
	}{{http.StatusForbidden, SearchErrorJSONDisabled}, {http.StatusTooManyRequests, SearchErrorRateLimited}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", test.status) }))
		_, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "q")
		server.Close()
		var searchErr *SearchError
		if !errors.As(err, &searchErr) || searchErr.Code != test.code {
			t.Fatalf("status %d error=%v", test.status, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not-json")) }))
	defer server.Close()
	if _, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "q"); err == nil {
		t.Fatal("invalid JSON returned nil error")
	} else {
		var searchErr *SearchError
		if !errors.As(err, &searchErr) || searchErr.Code != SearchErrorInvalidResponse {
			t.Fatalf("invalid JSON error=%v", err)
		}
	}
}

func TestWebSearcherRejectsEmptyResultsAndReportsUnresponsiveEngines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[["duckduckgo","timeout"]]}`))
	}))
	defer server.Close()
	_, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "q")
	var searchErr *SearchError
	if !errors.As(err, &searchErr) || searchErr.Code != SearchErrorEnginesUnresponsive || len(searchErr.UnresponsiveEngines) != 1 {
		t.Fatalf("empty results error=%v", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"results":[]}`)) }))
	defer empty.Close()
	_, err = NewWebSearcher(empty.URL, 5).Search(context.Background(), "q")
	if !errors.As(err, &searchErr) || searchErr.Code != SearchErrorEmptyResults {
		t.Fatalf("empty results code=%v", err)
	}
}

func TestWebSearcherDeduplicatesURLsAndRejectsInvalidResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"one","content":"a","url":"https://example.com/1"},{"title":"duplicate","content":"b","url":"https://example.com/1"},{"title":"","content":"c","url":"https://example.com/2"},{"title":"bad","content":"d","url":"not-a-url"},{"title":"two","content":"e","url":"https://example.com/2"}]}`))
	}))
	defer server.Close()
	got, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "q")
	if err != nil || len(got) != 2 || got[0].URL != "https://example.com/1" || got[1].URL != "https://example.com/2" {
		t.Fatalf("deduplicated results=%#v err=%v", got, err)
	}
}

func TestWebSearcherUsesInfoboxWhenResultsAreEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[],"infoboxes":[{"infobox":"Boxer","id":"https://example.com/boxer","content":"career facts","urls":[]}]}`))
	}))
	defer server.Close()
	got, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "boxer")
	if err != nil || len(got) != 1 || got[0].URL != "https://example.com/boxer" {
		t.Fatalf("results=%#v err=%v", got, err)
	}
}

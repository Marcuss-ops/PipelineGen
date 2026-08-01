package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebSearcherSearXNGJSONAndLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" {
			t.Fatalf("bad request: %s", r.URL.String())
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

func TestWebSearcherRejectsBadJSONAndHTTPError(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", status) }))
		_, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "q")
		server.Close()
		if err == nil {
			t.Fatalf("status %d returned nil error", status)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not-json")) }))
	defer server.Close()
	if _, err := NewWebSearcher(server.URL, 5).Search(context.Background(), "q"); err == nil {
		t.Fatal("invalid JSON returned nil error")
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

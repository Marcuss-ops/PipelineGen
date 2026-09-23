package veloxclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClipExists_CallsCanonicalRouteAndDecodes pins the T1.3 client arm:
// the probe MUST hit RouteClipsExists with the url query escaped, and the
// {exists, clip_id} envelope must decode losslessly.
func TestClipExists_CallsCanonicalRouteAndDecodes(t *testing.T) {
	const wantURL = "https://www.youtube.com/watch?v=506AyzC7d-k"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RouteClipsExists {
			t.Errorf("path = %s, want %s", r.URL.Path, RouteClipsExists)
		}
		if got := r.URL.Query().Get("url"); got != wantURL {
			t.Errorf("url query = %q, want %q", got, wantURL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"exists":true,"clip_id":"yt_506AyzC7d-k"}`))
	}))
	defer server.Close()

	c := New(server.URL, "")
	resp, err := c.ClipExists(context.Background(), wantURL)
	if err != nil {
		t.Fatalf("ClipExists: %v", err)
	}
	if !resp.OK || !resp.Exists || resp.ClipID != "yt_506AyzC7d-k" {
		t.Errorf("resp = %+v, want OK+exists with clip id", resp)
	}
}

// TestClipExists_EmptyURLFailsClientSide pins the input guard: an empty
// url never reaches the wire.
func TestClipExists_EmptyURLFailsClientSide(t *testing.T) {
	c := New("http://127.0.0.1:0", "")
	if _, err := c.ClipExists(context.Background(), "  "); err == nil {
		t.Fatal("empty url must fail client-side")
	}
}

package veloxclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRateLimited429_ReturnsErrRateLimitedWrappingBadRequest pins the T1.1
// transport classification: a 429 MUST be detectable as congestion
// (errors.Is ErrRateLimited) while STILL satisfying every existing
// errors.Is(err, ErrBadRequest) caller — the sentinel wraps the old one, so
// adding the distinction cannot change legacy retry decisions.
func TestRateLimited429_ReturnsErrRateLimitedWrappingBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"kind":"rate_limited","error":"slow down","retry_after_seconds":2}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "")
	_, err := client.ClipInfo(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err == nil {
		t.Fatal("429 must surface as an error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("errors.Is(err, ErrRateLimited) = false, want true (429 must be typed as congestion); err=%v", err)
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("errors.Is(err, ErrBadRequest) = false — the sentinel MUST wrap it for legacy callers; err=%v", err)
	}
	if errors.Is(err, ErrServer) {
		t.Errorf("429 must not masquerade as ErrServer; err=%v", err)
	}
}

// TestBadRequest400_IsNotRateLimited pins the other direction: an ordinary
// 400 stays a plain ErrBadRequest so callers cannot mistake a malformed
// request for congestion.
func TestBadRequest400_IsNotRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, "")
	_, err := client.ClipInfo(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err == nil {
		t.Fatal("400 must surface as an error")
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("errors.Is(err, ErrBadRequest) = false, want true; err=%v", err)
	}
	if errors.Is(err, ErrRateLimited) {
		t.Errorf("a 400 must NOT be ErrRateLimited; err=%v", err)
	}
}

// TestNotFound404_Precedence keeps the 404 arm ahead of the generic 4xx
// classification after adding the 429 arm (switch-order regression guard).
func TestNotFound404_Precedence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := New(srv.URL, "")
	_, err := client.ClipInfo(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("errors.Is(err, ErrNotFound) = false, want true; err=%v", err)
	}
	if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrBadRequest) {
		t.Errorf("404 must stay ErrNotFound only; err=%v", err)
	}
}

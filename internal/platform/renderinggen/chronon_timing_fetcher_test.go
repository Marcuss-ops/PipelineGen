package renderinggen

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestChrononTimingFetcherFetchesByContentAddress pins the object-store
// contract: a storage-key reference is addressed as /objects/<key>.
func TestChrononTimingFetcherFetchesByContentAddress(t *testing.T) {
	body := []byte(`{"schema":"chronon3d.frame-timing.v1","summary":{}}`)
	key := digest.SHA256Bytes(body)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	fetcher, err := NewChrononTimingFetcher(srv.URL)
	if err != nil {
		t.Fatalf("NewChrononTimingFetcher: %v", err)
	}
	got, err := fetcher.FetchChrononTiming(context.Background(), key, "")
	if err != nil {
		t.Fatalf("FetchChrononTiming: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body = %q, want the served bytes", got)
	}
	if want := "/objects/" + key; gotPath != want {
		t.Fatalf("request path = %q, want %q", gotPath, want)
	}
}

// TestChrononTimingFetcherRejectsDigestMismatch pins that bytes served under a
// content address are re-hashed: a store that returns different bytes than the
// address promises is rejected, never recorded.
func TestChrononTimingFetcherRejectsDigestMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered bytes"))
	}))
	defer srv.Close()

	fetcher, err := NewChrononTimingFetcher(srv.URL)
	if err != nil {
		t.Fatalf("NewChrononTimingFetcher: %v", err)
	}
	address := digest.SHA256Bytes([]byte("the real sidecar"))
	if _, err := fetcher.FetchChrononTiming(context.Background(), address, ""); err == nil {
		t.Fatal("FetchChrononTiming accepted bytes that do not hash to the content address")
	}
}

// TestChrononTimingFetcherPrefersTheCertifiedURL pins that the URL
// RenderingGen certified wins over the store root.
func TestChrononTimingFetcherPrefersTheCertifiedURL(t *testing.T) {
	body := []byte(`{"schema":"chronon3d.frame-timing.v1"}`)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	fetcher, err := NewChrononTimingFetcher("http://unused.invalid")
	if err != nil {
		t.Fatalf("NewChrononTimingFetcher: %v", err)
	}
	if _, err := fetcher.FetchChrononTiming(context.Background(), "", srv.URL+"/objects/direct"); err != nil {
		t.Fatalf("FetchChrononTiming: %v", err)
	}
	if gotPath != "/objects/direct" {
		t.Fatalf("request path = %q, want the certified URL path", gotPath)
	}
}

// TestChrononTimingFetcherReportsUnfetchableReferences pins the typed
// sentinel: a reference with neither URL nor storage key is a broken producer
// contract, not a silent empty success.
func TestChrononTimingFetcherReportsUnfetchableReferences(t *testing.T) {
	fetcher, err := NewChrononTimingFetcher("http://store:9000")
	if err != nil {
		t.Fatalf("NewChrononTimingFetcher: %v", err)
	}
	if _, err := fetcher.FetchChrononTiming(context.Background(), "   ", "   "); !errors.Is(err, ErrChrononTimingUnavailable) {
		t.Fatalf("err = %v, want ErrChrononTimingUnavailable", err)
	}
}

// TestChrononTimingFetcherSurfacesHTTPFailures pins that a missing sidecar is
// an error (the projection then logs and skips), never a fake empty document.
func TestChrononTimingFetcherSurfacesHTTPFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	fetcher, err := NewChrononTimingFetcher(srv.URL)
	if err != nil {
		t.Fatalf("NewChrononTimingFetcher: %v", err)
	}
	if _, err := fetcher.FetchChrononTiming(context.Background(), "missing-key", ""); err == nil {
		t.Fatal("FetchChrononTiming returned nil error for HTTP 404")
	}
}

// TestNewChrononTimingFetcherRejectsEmptyStoreURL pins the fail-fast
// constructor (godlike/07: never a silent no-op).
func TestNewChrononTimingFetcherRejectsEmptyStoreURL(t *testing.T) {
	if _, err := NewChrononTimingFetcher("   "); err == nil {
		t.Fatal("NewChrononTimingFetcher(empty) = nil error, want fail-fast")
	}
}

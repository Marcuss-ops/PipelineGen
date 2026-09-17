package renderinggen

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// objectStoreStub records the bytes a prefetch publishes for one address.
type objectStoreStub struct {
	hash   string
	put    []byte
	putHit bool
}

func (s *objectStoreStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+s.hash {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			http.NotFound(w, r)
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.put = body
			s.putHit = true
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
}

func hashOf(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func writeTempFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestPrefetch_RejectsLocalHintWhoseBytesDoNotMatchTheAddress is the regression
// test for the reported failure: the DB claimed one content hash while the
// LocalPath held different bytes. The stale hint must be discarded and the
// verified download source used, so wrong bytes can never be published under
// the address (which would corrupt the content-addressed store and render the
// wrong image).
func TestPrefetch_RejectsLocalHintWhoseBytesDoNotMatchTheAddress(t *testing.T) {
	correct := []byte("the bytes the content address actually names")
	hash := hashOf(correct)

	localPath := writeTempFile(t, t.TempDir(), "stale.jpg", []byte("some other file entirely"))

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(correct)
	}))
	defer origin.Close()

	store := &objectStoreStub{hash: hash}
	srv := store.server()
	defer srv.Close()

	if err := NewHTTPAssetPrefetcher(srv.URL).Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: hash, LocalPath: localPath, SourceURL: origin.URL,
	}}); err != nil {
		t.Fatalf("prefetch: %v", err)
	}

	if !store.putHit {
		t.Fatal("the verified URL source should have been staged")
	}
	if string(store.put) != string(correct) {
		t.Errorf("published bytes = %q, want the bytes named by the address", string(store.put))
	}
	if hashOf(store.put) != hash {
		t.Error("published bytes do not hash to the address they were stored under")
	}
}

// TestPrefetch_RejectsMissingLocalHintAndFallsBackToURL: a deleted staging file
// is an expected event (tmpfs sweeps), not a staged render failure.
func TestPrefetch_RejectsMissingLocalHintAndFallsBackToURL(t *testing.T) {
	correct := []byte("payload fetched from the verified source")
	hash := hashOf(correct)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(correct)
	}))
	defer origin.Close()

	store := &objectStoreStub{hash: hash}
	srv := store.server()
	defer srv.Close()

	missing := filepath.Join(t.TempDir(), "never-written.jpg")
	if err := NewHTTPAssetPrefetcher(srv.URL).Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: hash, LocalPath: missing, SourceURL: origin.URL,
	}}); err != nil {
		t.Fatalf("a missing local staging hint must not fail the prefetch: %v", err)
	}
	if string(store.put) != string(correct) {
		t.Errorf("published bytes = %q, want the verified source bytes", string(store.put))
	}
}

// TestPrefetch_FailsClosedWhenNoVerifiedSourceExists: a rejected hint with no
// fetchable fallback must surface a typed, attributable error instead of being
// silently skipped and resurfacing as a mystery cache miss on the worker.
func TestPrefetch_FailsClosedWhenNoVerifiedSourceExists(t *testing.T) {
	hash := hashOf([]byte("expected bytes"))
	localPath := writeTempFile(t, t.TempDir(), "mismatch.jpg", []byte("different bytes"))

	store := &objectStoreStub{hash: hash}
	srv := store.server()
	defer srv.Close()

	err := NewHTTPAssetPrefetcher(srv.URL).Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: hash, LocalPath: localPath, URL: "assets/fonts/non-http-logical-path.ttf",
	}})
	if err == nil {
		t.Fatal("expected a fail-closed error, got nil")
	}
	if !errors.Is(err, ErrAssetSourceUnavailable) {
		t.Errorf("error = %v, want ErrAssetSourceUnavailable", err)
	}
	if store.putHit {
		t.Error("nothing may be published when no source verifies against the address")
	}
}

// TestVerifiedLocalPath pins the helper that enforces the boundary.
func TestVerifiedLocalPath(t *testing.T) {
	payload := []byte("verified bytes")
	hash := hashOf(payload)
	dir := t.TempDir()
	good := writeTempFile(t, dir, "good.bin", payload)
	bad := writeTempFile(t, dir, "bad.bin", []byte("nope"))

	cases := []struct {
		name string
		path string
		hash string
		want string
	}{
		{"matching bytes", good, hash, good},
		{"mismatching bytes", bad, hash, ""},
		{"missing file", filepath.Join(dir, "absent.bin"), hash, ""},
		{"empty hint", "", hash, ""},
		{"empty address", good, "", ""},
		{"directory is not a file", dir, hash, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifiedLocalPath(tc.path, tc.hash); got != tc.want {
				t.Errorf("verifiedLocalPath(%q, %q) = %q, want %q", tc.path, tc.hash, got, tc.want)
			}
		})
	}
}

// TestVerifiedLocalPath_IsCaseInsensitiveAboutTheAddress: content addresses are
// hex, and producers have historically emitted upper-case digests. Case must
// not turn a valid file into a rejected one.
func TestVerifiedLocalPath_IsCaseInsensitiveAboutTheAddress(t *testing.T) {
	payload := []byte("case insensitive address")
	dir := t.TempDir()
	path := writeTempFile(t, dir, "payload.bin", payload)

	upper := ""
	for _, r := range hashOf(payload) {
		if r >= 'a' && r <= 'f' {
			upper += string(r - 'a' + 'A')
			continue
		}
		upper += string(r)
	}
	if got := verifiedLocalPath(path, upper); got != path {
		t.Errorf("verifiedLocalPath with upper-case address = %q, want %q", got, path)
	}
}

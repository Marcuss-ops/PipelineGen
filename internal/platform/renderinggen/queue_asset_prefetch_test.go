package renderinggen

import (
	"context"
	"crypto/sha256"
	"fmt"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func TestHTTPAssetPrefetcherStagesVerifiedLocalAsset(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "classic1.mp4")
	payload := []byte("verified local background")
	if err := os.WriteFile(localPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	var uploaded []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+hash {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			http.NotFound(w, r)
		case http.MethodPut:
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	prefetcher := NewHTTPAssetPrefetcher(srv.URL)
	err := prefetcher.Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: hash, LocalPath: localPath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != string(payload) {
		t.Fatalf("staged local bytes = %q, want %q", string(uploaded), string(payload))
	}
	if strings.Contains(string(uploaded), localPath) {
		t.Fatal("local path leaked into staged bytes")
	}
}

// stagingPrefetchStore models the object store's pending staging surface: a
// local PUT is hashed server-side, the digest is compared by the client, and
// only finalize makes the object visible. It counts direct PUTs separately so
// a regression to hash-then-PUT is observable.
type stagingPrefetchStore struct {
	hash        string
	body        []byte
	stagingPuts int
	finalizes   int
	directPuts  int
	deletes     int
}

func (s *stagingPrefetchStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/objects/"+s.hash && r.Method == http.MethodHead:
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/objects/staging/"):
			switch {
			case strings.HasSuffix(r.URL.Path, "/finalize") && r.Method == http.MethodPost:
				s.finalizes++
				w.WriteHeader(http.StatusCreated)
			case r.Method == http.MethodPut:
				s.stagingPuts++
				s.body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprintf(w, `{"sha256":%q,"size_bytes":%d}`, hashOf(s.body), len(s.body))
			case r.Method == http.MethodDelete:
				s.deletes++
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case r.URL.Path == "/objects/"+s.hash && r.Method == http.MethodPut:
			s.directPuts++
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestHTTPAssetPrefetcherStagesLocalAssetThroughPendingSession(t *testing.T) {
	payload := []byte("local bytes read once by the staging upload")
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	localPath := filepath.Join(t.TempDir(), "staged.mp4")
	if err := os.WriteFile(localPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &stagingPrefetchStore{hash: hash}
	srv := httptest.NewServer(store.handler())
	defer srv.Close()

	if err := NewHTTPAssetPrefetcher(srv.URL).Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: hash, LocalPath: localPath,
	}}); err != nil {
		t.Fatalf("prefetch through staging: %v", err)
	}
	if store.stagingPuts != 1 || store.finalizes != 1 {
		t.Fatalf("staging PUTs=%d finalizes=%d, want 1 and 1", store.stagingPuts, store.finalizes)
	}
	if store.directPuts != 0 || store.deletes != 0 {
		t.Fatalf("direct PUTs=%d deletes=%d, want 0 and 0", store.directPuts, store.deletes)
	}
	if string(store.body) != string(payload) {
		t.Fatalf("staged bytes = %q, want %q", string(store.body), string(payload))
	}
}

func TestHTTPAssetPrefetcherStagesCanonicalPresetFont(t *testing.T) {
	font, err := ResolveFontAsset(FontPoppinsBold)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(font.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+font.Hash {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			http.NotFound(w, r)
		case http.MethodPut:
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	prefetcher := NewHTTPAssetPrefetcher(srv.URL)
	err = prefetcher.Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: font.Hash, URL: caprender.CanonicalPresetFontPath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != string(payload) {
		t.Fatalf("staged preset font bytes = %d, want %d", len(uploaded), len(payload))
	}
}

func TestHTTPAssetPrefetcherStagesCanonicalCyrillicFont(t *testing.T) {
	font, err := ResolveFontAsset(FontDejaVuSans)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(font.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/objects/"+font.Hash {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			http.NotFound(w, r)
		case http.MethodPut:
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	prefetcher := NewHTTPAssetPrefetcher(srv.URL)
	err = prefetcher.Prefetch(context.Background(), []scriptgen.RenderQueueAsset{{
		SHA256: font.Hash, URL: caprender.CanonicalTextFontPath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != string(payload) {
		t.Fatalf("staged DejaVuSans bytes differ from canonical asset")
	}
}

package renderinggen

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestMain gives the package a hermetic default object store. Submit paths
// prefetch their assets before enqueueing, and prefetchClipAssets otherwise
// falls back to the production-shaped http://127.0.0.1:9000. On a developer
// host or CI runner that happens to have something listening there, a unit
// test would issue a real HEAD and fail on its 401/404 instead of exercising
// the code under test. Tests that assert probe/upload behaviour still override
// RENDERINGGEN_STORE_URL with their own recorder via t.Setenv.
func TestMain(m *testing.M) {
	if os.Getenv("RENDERINGGEN_STORE_URL") != "" {
		os.Exit(m.Run())
	}
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			// Every content address is already staged. Prefetch's documented
			// fast path then skips the upload, which is irrelevant to the
			// callers under test and keeps the server read-only.
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	os.Setenv("RENDERINGGEN_STORE_URL", store.URL)
	code := m.Run()
	store.Close()
	os.Exit(code)
}

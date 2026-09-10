package renderinggen

import (
	"context"
	"crypto/sha256"
	"fmt"
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
		Hash: hash, LocalPath: localPath,
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

package assembly

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	contract "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
)

func TestFileCacheDownloadsVerifiesAndHitsCache(t *testing.T) {
	body := []byte("valid clip bytes")
	h := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	root := t.TempDir()
	c, err := NewFileCache(root)
	if err != nil {
		t.Fatal(err)
	}
	c.FFprobe = "true"
	a := contract.AssetRequirement{AssetID: "clip-1", Kind: "source_clip", SHA256: "sha256:" + hex.EncodeToString(h[:]), Location: server.URL, Availability: contract.AvailabilityKnown, Required: true}
	ok, err := c.Prepare(context.Background(), a)
	if err != nil || !ok {
		t.Fatalf("first prepare: ok=%v err=%v", ok, err)
	}
	ok, err = c.Prepare(context.Background(), a)
	if err != nil || !ok {
		t.Fatalf("cache prepare: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(root, hex.EncodeToString(h[:])+".media")); err != nil {
		t.Fatal(err)
	}
}

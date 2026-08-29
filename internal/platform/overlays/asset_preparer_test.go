package overlays

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetPreparer_DeduplicatesAssets(t *testing.T) {
	body := []byte("shared asset")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write(body)
	}))
	defer server.Close()

	cache, err := NewCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := NewAssetPreparer(cache)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := preparer.Prepare(context.Background(), []AssetRef{
		{AssetID: "a", URL: server.URL, SHA256: hash},
		{AssetID: "b", URL: server.URL, SHA256: hash},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || requests != 1 {
		t.Fatalf("paths=%v requests=%d, want one deduplicated download", paths, requests)
	}
}

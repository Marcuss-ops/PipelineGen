package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

func TestDownloadFinalArtifactVerifiesAndReuses(t *testing.T) {
	payload := []byte("the-final-composite-mp4-bytes")
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-admin-token" {
			t.Errorf("Authorization=%q, want admin bearer", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		hits.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	path, size, gotSHA, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-download-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if size != int64(len(payload)) || gotSHA != wantSHA {
		t.Fatalf("size=%d sha=%s, want %d/%s", size, gotSHA, len(payload), wantSHA)
	}
	downloaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != string(payload) {
		t.Fatalf("downloaded bytes changed: %q", downloaded)
	}

	// A retry with the same job/sha must reuse the verified file instead of
	// transferring hundreds of megabytes again.
	if _, _, _, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-download-test"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("artifact was fetched %d times, want reuse after the first", hits.Load())
	}
}

func TestDownloadFinalArtifactRejectsHashMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()
	adapter := &RemoteFinalJobAdapter{
		artifactToken: "test-admin-token",
		client:        &remotejob.Client{HTTP: server.Client()},
	}
	wantSHA := strings.Repeat("0", 64)
	if _, _, _, err := adapter.downloadFinalArtifact(context.Background(), server.URL, wantSHA, "job-hash-mismatch"); err == nil {
		t.Fatal("expected sha256 mismatch to fail closed")
	}
}

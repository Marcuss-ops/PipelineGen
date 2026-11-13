package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func finalArtifactTestServer(t *testing.T, payload []byte, receiptSHA string) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var srv *httptest.Server
	var auth atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/api/jobs/job_parent/full", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"SUCCEEDED","result":{"result":{"remote_final_job":{"job_id":"remote_1","status":"SUCCEEDED","artifact_url":"%s/artifact/final","sha256":"%s"}}}}`, srv.URL, receiptSHA)
	})
	mux.HandleFunc("/artifact/final", func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &auth
}

func setFinalDownloadEnv(t *testing.T, srv *httptest.Server) {
	t.Helper()
	t.Setenv("VELOX_BASE_URL", srv.URL)
	t.Setenv("VELOX_ADMIN_TOKEN", "admin-token")
	t.Setenv("VELOX_MASTER_ADMIN_TOKEN", "artifact-token")
	t.Setenv("VELOX_ARTIFACT_TOKEN", "")
	t.Setenv("VELOX_MASTER_TOKEN_FILE", "")
	t.Setenv("VELOX_FINAL_JOB_CREDENTIALS_FILE", "")
	t.Setenv("VELOX_HOME", t.TempDir())
}

func TestDownloadFinalUsesReceiptAndVerifiesSHA(t *testing.T) {
	payload := []byte("final-video-bytes")
	sum := sha256.Sum256(payload)
	srv, auth := finalArtifactTestServer(t, payload, fmt.Sprintf("%x", sum))
	setFinalDownloadEnv(t, srv)

	out := filepath.Join(t.TempDir(), "final.mp4")
	code, stdout, stderr := capture(t, func() int {
		return run([]string{"download-final", "job_parent", "-o", out})
	})
	if code != exitOK {
		t.Fatalf("download-final exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got, _ := auth.Load().(string); got != "Bearer artifact-token" {
		t.Fatalf("artifact bearer=%q, want Bearer artifact-token", got)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read downloaded final: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded bytes=%q, want %q", got, payload)
	}
	if !strings.Contains(stdout, "sha256") || !strings.Contains(stdout, "remote") {
		t.Fatalf("download output lacks receipt identity: %q", stdout)
	}
}

func TestDownloadFinalRejectsSHA256Mismatch(t *testing.T) {
	payload := []byte("tampered-bytes")
	expected := sha256.Sum256([]byte("expected-bytes"))
	srv, _ := finalArtifactTestServer(t, payload, fmt.Sprintf("%x", expected))
	setFinalDownloadEnv(t, srv)

	out := filepath.Join(t.TempDir(), "final.mp4")
	code, _, stderr := capture(t, func() int {
		return run([]string{"download-final", "job_parent", "-o", out})
	})
	if code != exitFailure {
		t.Fatalf("download-final exit=%d, want %d (stderr=%q)", code, exitFailure, stderr)
	}
	if !strings.Contains(stderr, "sha256 mismatch") {
		t.Fatalf("stderr does not explain the hash failure: %q", stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a failed hash verification must not publish the output, stat err=%v", err)
	}
}

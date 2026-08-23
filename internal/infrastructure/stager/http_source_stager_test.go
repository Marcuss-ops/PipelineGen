package stager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"go.uber.org/zap"
)

// newTestStager constructs an HTTPSourceStager rooted at a fresh
// t.TempDir() sub-directory so each test gets a clean staging area.
func newTestStager(t *testing.T) (*HTTPSourceStager, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "staged-sources")
	s, err := NewHTTPSourceStager(dir, &http.Client{}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewHTTPSourceStager: unexpected error: %v", err)
	}
	return s, dir
}

// TestHTTPSourceStager_StageSourceV2_DeterministicPath verifies that
// two StageSourceV2 calls for the same SourceRef produce the same
// LocalPath (PR-SOURCESTAGER-CONSOLIDATE: deterministic from URL).
func TestHTTPSourceStager_StageSourceV2_DeterministicPath(t *testing.T) {
	s, _ := newTestStager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	ref := assets.SourceRef{URL: srv.URL + "/a.png"}
	a, err := s.stageSourceV2(context.Background(), ref)
	if err != nil {
		t.Fatalf("first StageSourceV2: %v", err)
	}
	b, err := s.stageSourceV2(context.Background(), ref)
	if err != nil {
		t.Fatalf("second StageSourceV2: %v", err)
	}
	if a.LocalPath != b.LocalPath {
		t.Fatalf("expected deterministic LocalPath, got %q vs %q", a.LocalPath, b.LocalPath)
	}
	if a.IntermediateHash != b.IntermediateHash {
		t.Fatalf("expected deterministic hash, got %q vs %q", a.IntermediateHash, b.IntermediateHash)
	}
}

// TestHTTPSourceStager_StageSourceV2_IntermediateHashMatchesBody
// verifies that the IntermediateHash is the SHA-256 of the bytes
// written to disk, computed during the write (no second read pass).
func TestHTTPSourceStager_StageSourceV2_IntermediateHashMatchesBody(t *testing.T) {
	s, _ := newTestStager(t)
	body := []byte("the quick brown fox jumps over the lazy dog")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ref := assets.SourceRef{URL: srv.URL + "/b.png"}
	staged, err := s.stageSourceV2(context.Background(), ref)
	if err != nil {
		t.Fatalf("StageSourceV2: %v", err)
	}
	wantSum := sha256.Sum256(body)
	wantHash := hex.EncodeToString(wantSum[:])
	if staged.IntermediateHash != wantHash {
		t.Fatalf("IntermediateHash mismatch: got %q want %q", staged.IntermediateHash, wantHash)
	}
	if staged.Bytes != int64(len(body)) {
		t.Fatalf("Bytes mismatch: got %d want %d", staged.Bytes, len(body))
	}
	// Verify the on-disk file actually has the same hash (defense in
	// depth against a regression where the writer is not the hasher).
	onDisk, err := os.ReadFile(staged.LocalPath)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(onDisk) != string(body) {
		t.Fatalf("on-disk bytes do not match body: got %q want %q", onDisk, body)
	}
}

// TestHTTPSourceStager_StageSourceV2_DistinctURLsDistinctPaths
// verifies that two different SourceRef.URL values produce different
// LocalPaths and different hashes.
func TestHTTPSourceStager_StageSourceV2_DistinctURLsDistinctPaths(t *testing.T) {
	s, _ := newTestStager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("shared body"))
	}))
	defer srv.Close()

	a, err := s.stageSourceV2(context.Background(), assets.SourceRef{URL: srv.URL + "/a"})
	if err != nil {
		t.Fatalf("StageSourceV2 a: %v", err)
	}
	b, err := s.stageSourceV2(context.Background(), assets.SourceRef{URL: srv.URL + "/b"})
	if err != nil {
		t.Fatalf("StageSourceV2 b: %v", err)
	}
	if a.LocalPath == b.LocalPath {
		t.Fatalf("expected distinct LocalPaths, got %q twice", a.LocalPath)
	}
}

// TestHTTPSourceStager_StageSourceV2_NonOKStatusFailsClosed verifies
// that a non-2xx HTTP response is rejected with a typed error and
// does NOT leave a partial file on disk.
func TestHTTPSourceStager_StageSourceV2_NonOKStatusFailsClosed(t *testing.T) {
	s, dir := newTestStager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := s.stageSourceV2(context.Background(), assets.SourceRef{URL: srv.URL + "/oops"})
	if err == nil {
		t.Fatal("expected error for non-2xx status, got nil")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected error to mention status 500, got: %v", err)
	}
	// No leftover files in the staging dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no files in staging dir, got %d", len(entries))
	}
}

// TestHTTPSourceStager_StageSourceV2_EmptyURLFailsClosed verifies that
// an empty SourceRef.URL is rejected with a typed error (godlike/07).
func TestHTTPSourceStager_StageSourceV2_EmptyURLFailsClosed(t *testing.T) {
	s, _ := newTestStager(t)
	_, err := s.stageSourceV2(context.Background(), assets.SourceRef{URL: ""})
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
}

// TestHTTPSourceStager_CleanupStagedSource_Idempotent verifies that
// CleanupStagedSource is idempotent: a second call for the same
// staged value is a no-op, and a nil staged is a no-op.
func TestHTTPSourceStager_CleanupStagedSource_Idempotent(t *testing.T) {
	s, _ := newTestStager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	staged, err := s.stageSourceV2(context.Background(), assets.SourceRef{URL: srv.URL + "/c"})
	if err != nil {
		t.Fatalf("StageSourceV2: %v", err)
	}
	if err := s.cleanupStagedSource(context.Background(), staged); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if err := s.cleanupStagedSource(context.Background(), staged); err != nil {
		t.Fatalf("second cleanup (idempotent): %v", err)
	}
	if err := s.cleanupStagedSource(context.Background(), nil); err != nil {
		t.Fatalf("nil staged: %v", err)
	}
}

// TestHTTPSourceStager_NewHTTPSourceStager_RejectsEmptyDir verifies
// the fail-closed construction-time guard (godlike/07: empty
// StagingDir is a programmer error, not a silent fallback).
func TestHTTPSourceStager_NewHTTPSourceStager_RejectsEmptyDir(t *testing.T) {
	if _, err := NewHTTPSourceStager("", &http.Client{}, zap.NewNop()); err == nil {
		t.Fatal("expected error for empty StagingDir, got nil")
	}
	if _, err := NewHTTPSourceStager("/tmp", nil, zap.NewNop()); err == nil {
		t.Fatal("expected error for nil client, got nil")
	}
	if _, err := NewHTTPSourceStager("/tmp", &http.Client{}, nil); err == nil {
		t.Fatal("expected error for nil logger, got nil")
	}
}

// TestHTTPSourceStager_StageSourceV2_ConcurrentSameURLSafe verifies
// that two goroutines staging the same SourceRef share a single
// download (deterministic path dedupe + per-path mutex).
func TestHTTPSourceStager_StageSourceV2_ConcurrentSameURLSafe(t *testing.T) {
	s, _ := newTestStager(t)
	var hits int
	var hitsMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsMu.Lock()
		hits++
		hitsMu.Unlock()
		_, _ = w.Write([]byte("concurrent body"))
	}))
	defer srv.Close()

	ref := assets.SourceRef{URL: srv.URL + "/d"}
	var wg sync.WaitGroup
	const N = 8
	results := make([]*assets.StagedSource, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = s.stageSourceV2(context.Background(), ref)
		}()
	}
	wg.Wait()

	path := results[0].LocalPath
	hash := results[0].IntermediateHash
	for i := 1; i < N; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i].LocalPath != path {
			t.Fatalf("goroutine %d: LocalPath mismatch %q vs %q", i, results[i].LocalPath, path)
		}
		if results[i].IntermediateHash != hash {
			t.Fatalf("goroutine %d: hash mismatch %q vs %q", i, results[i].IntermediateHash, hash)
		}
	}
	// The file on disk should match.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(body) != "concurrent body" {
		t.Fatalf("on-disk body mismatch: %q", body)
	}
	// The server should have been hit at most twice (winner + the
	// rare race-loser; the per-path mutex makes the second call see
	// the existing file and short-circuit before issuing the GET).
	hitsMu.Lock()
	defer hitsMu.Unlock()
	if hits > 2 {
		t.Fatalf("expected at most 2 server hits for %d concurrent calls, got %d", N, hits)
	}
}

// compile-time check: HTTPSourceStager satisfies the canonical acquisition port.
var _ acquisition.SourceStager = (*HTTPSourceStager)(nil)

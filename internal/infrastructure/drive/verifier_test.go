// Package drive — verifier_test.go (Fase 10 / Commit 1, July 2026)
//
// 5 focused tests for the UploadVerifier (verifier.go):
//
//	#1 — happy path: mock returns 200 + non-trashed file →
//	     nil error + populated envelope (FileIDPresent=true,
//	     FileNotInTrash=true)
//	#2 — 404: mock returns 404 → ErrDriveFileNotFound wrapped
//	#3 — trashed: mock returns 200 + Trashed=true →
//	     ErrDriveFileInTrash wrapped
//	#4 — nil reader: NewUploadVerifier(nil).Verify(...) →
//	     wiring-error (NOT a panic)
//	#5 — empty fileID: Verify(ctx, "", params) →
//	     ErrDriveFileNotFound
//
// The mock is intentionally a SEPARATE httptest server from the
// existing uploader_ops_test.go mock (which handles Files.List +
// Files.Create) because the verifier's per-commit scope is
// Files.Get only. Future Fase 10 commits that need combined
// List+Get mocks can extract a shared helper; for Commit 1 the
// focused mock keeps the diff small and the test failure surface
// narrow.
//
// godlike/07 fail-closed: every assertion that the verifier
// returns a typed sentinel uses errors.Is (NOT substring
// matching) so future refactors that rewrap the sentinel via
// fmt.Errorf %w still satisfy the contract.
package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// urlRewritingVerifierTransport mirrors the pattern from
// uploader_ops_test.go::urlRewritingTransport: redirects every
// request's host:port to the mock server while preserving the
// path the Drive SDK constructed. Test-local (lowercase) so it
// doesn't leak into the package surface.
type urlRewritingVerifierTransport struct {
	mockHost   string
	mockScheme string
}

func (t *urlRewritingVerifierTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.mockScheme
	req.URL.Host = t.mockHost
	return http.DefaultTransport.RoundTrip(req)
}

// mockFilesGetServer wires an httptest server that mimics the
// Google Drive v3 Files.Get surface used by UploadVerifier.
// Each call to /drive/v3/files/{id} consumes the next canned
// response from a FIFO queue. The default response (when the
// queue is empty) is 200 + a non-trashed file matching the
// requested ID.
type mockFilesGetServer struct {
	*httptest.Server
	mu        sync.Mutex
	responses []rawGetResp // FIFO of canned Files.Get responses
}

type rawGetResp struct {
	status int
	body   string
}

func newMockFilesGetServer() *mockFilesGetServer {
	srv := &mockFilesGetServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drive's Files.Get is /drive/v3/files/{id} (no
		// /upload prefix). List is /drive/v3/files?q=...;
		// we don't handle List here — Commit 1 scope is Get
		// only. If a future commit needs List+Get, the
		// existing uploader_ops_test.go mock has the
		// canonical surface.
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/drive/v3/files" {
			// Files.List — not in Commit 1 scope; return
			// empty so the verifier's GetFileMeta (which
			// does NOT call List) doesn't trip.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":[]}`))
			return
		}
		// Anything else under /drive/v3/files/* is a
		// Files.Get.
		srv.mu.Lock()
		defer srv.mu.Unlock()
		if len(srv.responses) > 0 {
			rr := srv.responses[0]
			srv.responses = srv.responses[1:]
			w.Header().Set("Content-Type", "application/json")
			if rr.status != http.StatusOK {
				w.WriteHeader(rr.status)
			}
			_, _ = w.Write([]byte(rr.body))
			return
		}
		// Default: 200 with a non-trashed file matching
		// the requested ID. The body shape matches what
		// ffprobe.Files.Get returns with the
		// (id, name, mimeType, size, webViewLink, parents,
		// trashed) Fields() projection that
		// uploader_file.go::GetFileMeta requests.
		// Extract the file ID from the URL path for the
		// echo.
		id := r.URL.Path
		if len(id) > len("/drive/v3/files/") {
			id = id[len("/drive/v3/files/"):]
		}
		// Note: size is QUOTED (the canonical Drive API
		// convention: the driveapi.File struct uses
		// `json:"size,omitempty,string"` so the SDK
		// unmarshals a JSON number-as-string). An unquoted
		// int would trigger:
		//   "json: invalid use of ,string struct tag,
		//    trying to unmarshal unquoted value into int64"
		// — which surfaces as a Files.Get round-trip
		// failure inside GetFileMeta (NOT a verifier
		// bug; the mock would be wrong).
		body := fmt.Sprintf(`{"id":%q,"name":"clip.mp4","mimeType":"video/mp4","size":"1024","webViewLink":"https://drive.google.com/file/d/%s/view","parents":["root-folder"],"trashed":false}`, id, id)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return srv
}

func (s *mockFilesGetServer) attachMockService(t *testing.T) *driveapi.Service {
	t.Helper()
	mockURL, err := url.Parse(s.Server.URL)
	if err != nil {
		t.Fatalf("mockFilesGetServer.attachMockService: parse mock URL %q: %v", s.Server.URL, err)
	}
	httpClient := &http.Client{
		Transport: &urlRewritingVerifierTransport{
			mockHost:   mockURL.Host,
			mockScheme: mockURL.Scheme,
		},
	}
	svc, err := driveapi.NewService(context.Background(),
		option.WithHTTPClient(httpClient),
		option.WithoutAuthentication(),
		option.WithScopes(driveapi.DriveScope),
	)
	if err != nil {
		t.Fatalf("mockFilesGetServer.attachMockService: driveapi.NewService: %v", err)
	}
	return svc
}

// ── Test #1: Happy path ───────────────────────────────────────────────

// TestUploadVerifier_HappyPath pins the Commit-1 contract: when
// the mock Files.Get returns a non-trashed file, the verifier
// returns nil error + a populated UploadVerification (FileIDPresent
// and FileNotInTrash both true).
func TestUploadVerifier_HappyPath(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)

	v, err := verifier.Verify(context.Background(), "file-abc", VerificationParams{
		ExpectedName:     "clip.mp4",
		ExpectedFolderID: "root-folder",
	})
	if err != nil {
		t.Fatalf("expected nil error on happy path, got: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil UploadVerification on happy path")
	}
	if v.FileID != "file-abc" {
		t.Errorf("FileID echo: got %q, want %q", v.FileID, "file-abc")
	}
	if !v.FileIDPresent {
		t.Error("FileIDPresent must be true on happy path (Files.Get returned 200 with non-empty id)")
	}
	if !v.FileNotInTrash {
		t.Error("FileNotInTrash must be true on happy path (Trashed=false in response)")
	}
	if v.Meta == nil {
		t.Fatal("Meta must be populated on happy path (caller uses it for downstream checks)")
	}
	if v.Meta.ID != "file-abc" {
		t.Errorf("Meta.ID: got %q, want %q (must echo the requested file ID)", v.Meta.ID, "file-abc")
	}
}

// ── Test #2: 404 → ErrDriveFileNotFound ────────────────────────────────

// TestUploadVerifier_404_NotFound pins the typed-sentinel contract
// for the "file not found" case. The mock returns 404; the
// verifier MUST surface ErrDriveFileNotFound (via errors.Is, NOT
// substring match) so the caller can distinguish "missing file"
// from "API error" from "file in trash".
func TestUploadVerifier_404_NotFound(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()
	srv.responses = []rawGetResp{
		{status: 404, body: `{"error":{"code":404,"message":"File not found"}}`},
	}

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)

	v, err := verifier.Verify(context.Background(), "missing-file", VerificationParams{})
	if !errors.Is(err, ErrDriveFileNotFound) {
		t.Fatalf("expected ErrDriveFileNotFound on 404, got: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil UploadVerification on 404 (caller can log the probed ID)")
	}
	if v.FileIDPresent {
		t.Error("FileIDPresent must be false on 404")
	}
	if v.FileNotInTrash {
		t.Error("FileNotInTrash must be false on 404")
	}
}

// ── Test #3: Trashed → ErrDriveFileInTrash ─────────────────────────────

// TestUploadVerifier_Trashed pins the typed-sentinel contract
// for the "file in trash" case. The mock returns 200 with
// Trashed=true; the verifier MUST surface ErrDriveFileInTrash.
// This is the Commit-1 hard requirement: a trashed existing file
// (e.g. found via FindFileByIdempotencyKey) MUST NOT be
// silently accepted by the PutActionSkipped branch of PutFile.
func TestUploadVerifier_Trashed(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()
	srv.responses = []rawGetResp{
		{status: 200, body: `{"id":"trashed-file","name":"clip.mp4","mimeType":"video/mp4","size":"1024","webViewLink":"https://drive.google.com/file/d/trashed-file/view","parents":["root-folder"],"trashed":true}`},
	}

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)

	v, err := verifier.Verify(context.Background(), "trashed-file", VerificationParams{})
	if !errors.Is(err, ErrDriveFileInTrash) {
		t.Fatalf("expected ErrDriveFileInTrash on trashed file, got: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil UploadVerification on trashed file (caller can log Trashed=true state)")
	}
	if !v.FileIDPresent {
		t.Error("FileIDPresent must be true on trashed file (file DOES exist; just in trash)")
	}
	if v.FileNotInTrash {
		t.Error("FileNotInTrash must be false on trashed file")
	}
	if v.Meta == nil {
		t.Fatal("Meta must be populated on trashed file (caller reads Trashed flag)")
	}
	if !v.Meta.Trashed {
		t.Error("Meta.Trashed must be true (mock returned trashed:true)")
	}
}

// ── Test #4: parent mismatch → typed destination-integrity error ───────

func TestUploadVerifier_NameMismatch(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()
	srv.responses = []rawGetResp{
		{status: http.StatusOK, body: `{"id":"file-name-mismatch","name":"actual.mp3","size":"1024","parents":["resolved-folder"],"trashed":false}`},
	}

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)
	_, err := verifier.Verify(context.Background(), "file-name-mismatch", VerificationParams{ExpectedName: "expected.mp3"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "name mismatch")
}

func TestUploadVerifier_IDMismatch(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()
	srv.responses = []rawGetResp{
		{status: http.StatusOK, body: `{"id":"different-file","name":"voice.mp3","size":"1024","parents":["resolved-folder"],"trashed":false}`},
	}

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)
	_, err := verifier.Verify(context.Background(), "uploaded-file", VerificationParams{})
	require.ErrorIs(t, err, ErrDriveFileIDMismatch)
}

func TestUploadVerifier_ParentMismatch(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()
	srv.responses = []rawGetResp{
		{status: http.StatusOK, body: `{"id":"file-wrong-parent","name":"voice.mp3","size":"1024","parents":["actual-folder"],"trashed":false}`},
	}

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)
	_, err := verifier.Verify(context.Background(), "file-wrong-parent", VerificationParams{ExpectedFolderID: "resolved-folder"})
	if !errors.Is(err, ErrDriveFileParentMismatch) {
		t.Fatalf("expected ErrDriveFileParentMismatch, got: %v", err)
	}
}

// ── Test #5: nil reader → wiring error ─────────────────────────────────

// TestUploadVerifier_NilReader pins the composition-root
// fail-closed contract: a verifier constructed with a nil
// Reader returns a wrapped error (NOT a panic) so a future
// composition-root wiring misconfig surfaces as a typed
// error rather than a mid-call panic. This matches the
// nil-safe contract on every other drive adapter.
func TestUploadVerifier_NilReader(t *testing.T) {
	verifier := NewUploadVerifier(nil)

	v, err := verifier.Verify(context.Background(), "any-id", VerificationParams{})
	if err == nil {
		t.Fatal("expected error on nil reader, got nil — composition-root wiring misconfig must fail-closed")
	}
	if v != nil {
		t.Errorf("expected nil UploadVerification on nil reader, got: %+v", v)
	}
}

// ── Test #6: Empty fileID → ErrDriveFileNotFound ───────────────────────

// TestUploadVerifier_EmptyFileID pins the empty-input contract:
// a zero-value fileID MUST surface as ErrDriveFileNotFound (NOT
// a Files.Get call with an empty ID, which Drive would reject
// with 400 Bad Request, AND NOT a panic). The caller doesn't
// have to special-case the empty-string path.
func TestUploadVerifier_EmptyFileID(t *testing.T) {
	srv := newMockFilesGetServer()
	defer srv.Server.Close()

	u := &Uploader{Service: srv.attachMockService(t), Log: nil}
	verifier := NewUploadVerifier(u)

	v, err := verifier.Verify(context.Background(), "", VerificationParams{})
	if !errors.Is(err, ErrDriveFileNotFound) {
		t.Fatalf("expected ErrDriveFileNotFound on empty fileID, got: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil UploadVerification on empty fileID (echoes the probed ID)")
	}
	if v.FileID != "" {
		t.Errorf("FileID echo: got %q, want \"\"", v.FileID)
	}
}

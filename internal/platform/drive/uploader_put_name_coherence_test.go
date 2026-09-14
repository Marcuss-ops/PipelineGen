// Package drive — uploader_put_name_coherence_test.go (Sept 2026)
//
// PR-STOCK-NAME-COHERENCE regression anchors.
//
// The Drive idempotency key identifies CONTENT (dest:artifactID:sha256:version),
// while the Drive display name is a per-run ordinal (Stock emits clip_%03d by
// preparation order). A re-run therefore reuses the file published by the first
// run — correct for byte-identity, wrong for the operator-visible folder: the
// reused file kept the OLD name, so a live run could publish content the
// manifest calls clip_008 inside a file named clip_006.
//
// Two pins:
//  1. ConflictSkip + existing match + DIFFERENT name → name-only Files.Update
//     (no re-upload) and Action=Updated with the canonical filename.
//  2. ConflictSkip + existing match + SAME name → pure skip, zero Drive writes.
package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// nameCoherenceDriveService builds a Drive service whose requests are rewritten
// onto the mock server, so the SDK's URL grammar stays its own business.
func nameCoherenceDriveService(t *testing.T, serverURL string) *driveapi.Service {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse mock server URL: %v", err)
	}
	client := &http.Client{Transport: &urlRewritingTransport{
		mockHost: parsed.Host, mockScheme: parsed.Scheme,
	}}
	service, err := driveapi.NewService(context.Background(),
		option.WithHTTPClient(client), option.WithoutAuthentication(),
		option.WithScopes(driveapi.DriveScope))
	if err != nil {
		t.Fatalf("create Drive service: %v", err)
	}
	return service
}

// nameCoherenceMock records the rename (metadata PATCH) calls it serves.
type nameCoherenceMock struct {
	*httptest.Server
	mu         sync.Mutex
	patches    []string
	uploadHits int
}

func newNameCoherenceMock(t *testing.T, existingID, renamedTo string) *nameCoherenceMock {
	t.Helper()
	m := &nameCoherenceMock{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/files/"):
			raw, _ := io.ReadAll(r.Body)
			m.mu.Lock()
			m.patches = append(m.patches, string(raw))
			m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + existingID + `","name":"` + renamedTo +
				`","webViewLink":"https://drive.google.com/file/d/` + existingID + `/view","md5Checksum":"content-hash"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/files/"):
			// The post-upload verifier re-reads the file (id + name + parent +
			// trash state) after an update — serve the renamed view.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + existingID + `","name":"` + renamedTo +
				`","parents":["folder-abc"],"trashed":false}`))
		case r.Method == http.MethodPost:
			m.mu.Lock()
			m.uploadHits++
			m.mu.Unlock()
			http.Error(w, "no upload expected on the reuse path", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *nameCoherenceMock) patchCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.patches)
}

func (m *nameCoherenceMock) patchBodies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.patches...)
}

// reuseLookup always reports the same pre-existing Drive file for the requested
// idempotency key — the reuse path a re-run takes for byte-identical content.
func reuseLookup(existingName string) func(*Uploader, context.Context, string, string, string) (ExistingFileLookup, error) {
	return func(*Uploader, context.Context, string, string, string) (ExistingFileLookup, error) {
		return ExistingFileLookup{Matches: []RemoteFile{{
			FileID:      "drive-existing-999",
			Name:        existingName,
			WebViewLink: "https://drive.google.com/file/d/drive-existing-999/view",
			MD5Checksum: "content-hash",
		}}}, nil
	}
}

// TestPutFile_ConflictSkip_ReusedFileRenamedToCanonicalFilename pins the fix:
// the reused file is renamed onto the run's canonical filename without any
// media re-upload.
func TestPutFile_ConflictSkip_ReusedFileRenamedToCanonicalFilename(t *testing.T) {
	mock := newNameCoherenceMock(t, "drive-existing-999", "clip_008.mp4")

	u := &Uploader{
		Service:    nameCoherenceDriveService(t, mock.URL),
		Log:        zap.NewNop(),
		lookupFunc: reuseLookup("clip_006.mp4"), // stale name from an earlier run
	}

	result, err := u.PutFile(context.Background(), PutFileRequest{
		LocalPath:      "/nonexistent/clip_008.mp4",
		FolderID:       "folder-abc",
		Filename:       "clip_008.mp4",
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: "idem-content-key",
	})
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if result.FileID != "drive-existing-999" {
		t.Fatalf("FileID = %q, want the reused file (content dedup must not create a new file)", result.FileID)
	}
	if result.Filename != "clip_008.mp4" {
		t.Fatalf("Filename = %q, want the canonical clip_008.mp4", result.Filename)
	}
	if result.Action != PutActionUpdated {
		t.Fatalf("Action = %q, want %q (the reused file was renamed)", result.Action, PutActionUpdated)
	}
	if got := mock.patchCount(); got != 1 {
		t.Fatalf("rename PATCH calls = %d, want 1", got)
	}
	if body := mock.patchBodies()[0]; !strings.Contains(body, "clip_008.mp4") {
		t.Fatalf("rename body %q must carry the canonical filename", body)
	}
	if mock.uploadHits != 0 {
		t.Fatalf("reuse path must not re-upload the payload (upload calls = %d)", mock.uploadHits)
	}
}

// TestPutFile_ConflictSkip_ReusedFileSameNameStaysPureSkip pins the negative
// branch: when the reused file already carries the canonical name, the path
// stays a zero-write skip (no spurious Drive mutations on every re-run).
func TestPutFile_ConflictSkip_ReusedFileSameNameStaysPureSkip(t *testing.T) {
	mock := newNameCoherenceMock(t, "drive-existing-999", "clip_008.mp4")

	u := &Uploader{
		Service:    nameCoherenceDriveService(t, mock.URL),
		Log:        zap.NewNop(),
		lookupFunc: reuseLookup("clip_008.mp4"),
	}

	result, err := u.PutFile(context.Background(), PutFileRequest{
		LocalPath:      "/nonexistent/clip_008.mp4",
		FolderID:       "folder-abc",
		Filename:       "clip_008.mp4",
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: "idem-content-key",
	})
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if result.Action != PutActionSkipped {
		t.Fatalf("Action = %q, want %q", result.Action, PutActionSkipped)
	}
	if got := mock.patchCount(); got != 0 {
		t.Fatalf("rename PATCH calls = %d, want 0 when the name already matches", got)
	}
	if mock.uploadHits != 0 {
		t.Fatalf("upload calls = %d, want 0", mock.uploadHits)
	}
}

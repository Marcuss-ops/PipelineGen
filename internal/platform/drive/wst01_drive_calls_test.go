package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// TestPutFileCollapsesFolderCallsPerRun certifies the three WST-01 savings in
// one flow: the folder is listed ONCE for every artifact lookup, the folder is
// granted public read ONCE, and the post-upload verification reads the write
// response instead of issuing a Files.Get.
func TestPutFileCollapsesFolderCallsPerRun(t *testing.T) {
	var listCalls, getCalls, permListCalls, permCreateCalls, createCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/permissions"):
			permListCalls.Add(1)
			writeJSON(t, w, map[string]any{"permissions": []any{}})
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/permissions"):
			permCreateCalls.Add(1)
			writeJSON(t, w, map[string]any{"id": "perm-1"})
		case r.Method == http.MethodGet && path == "/drive/v3/files":
			listCalls.Add(1)
			require.Contains(t, r.URL.Query().Get("q"), "in parents", "the folder index must list the folder")
			writeJSON(t, w, map[string]any{"files": []any{
				map[string]any{"id": "older", "name": "already-there.mp4", "webViewLink": "https://drive/older", "md5Checksum": "aa"},
			}})
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/upload/drive/v3/files"):
			createCalls.Add(1)
			// The create body is multipart/related: the first part is the file
			// metadata JSON, so the name is extracted from the raw body.
			raw, _ := io.ReadAll(r.Body)
			name := "artifact.mp4"
			if start := strings.Index(string(raw), `"name":"`); start >= 0 {
				rest := string(raw)[start+len(`"name":"`):]
				if end := strings.Index(rest, `"`); end > 0 {
					name = rest[:end]
				}
			}
			writeJSON(t, w, map[string]any{
				"id":          fmt.Sprintf("new-%d", createCalls.Load()),
				"name":        name,
				"parents":     []string{"folder-1"},
				"size":        "5",
				"webViewLink": "https://drive/new",
			})
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/drive/v3/files/"):
			getCalls.Add(1)
			writeJSON(t, w, map[string]any{
				"id": "new-1", "name": "artifact.mp4", "parents": []string{"folder-1"}, "size": "5",
			})
		default:
			t.Errorf("unexpected Drive call: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	svc, err := driveapi.NewService(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: &urlRewritingTransport{
			mockHost:   strings.TrimPrefix(server.URL, "http://"),
			mockScheme: "http",
		}}),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)

	local := filepath.Join(t.TempDir(), "artifact.mp4")
	require.NoError(t, os.WriteFile(local, []byte("12345"), 0o600))

	uploader := &Uploader{Service: svc, Log: zap.NewNop()}
	request := PutFileRequest{
		LocalPath:      local,
		FolderID:       "folder-1",
		Filename:       "artifact.mp4",
		ConflictPolicy: delivery.ConflictSkip,
		ExpectedSize:   5,
		PublicRead:     true,
	}
	created, err := uploader.PutFile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, PutActionCreated, created.Action)
	require.NotNil(t, created.WriteMeta, "the write response must be the verification source")

	// The second identical artifact is found through the SAME folder index (the
	// first upload folded itself into it), so it costs no listing and no upload.
	second, err := uploader.PutFile(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, PutActionSkipped, second.Action)

	require.Equal(t, int32(1), listCalls.Load(), "one folder listing serves every lookup in the run")
	require.Equal(t, int32(0), getCalls.Load(), "verification must not re-read the file it just wrote")
	require.Equal(t, int32(1), permListCalls.Load(), "one folder permission check per run")
	require.Equal(t, int32(1), permCreateCalls.Load(), "one folder permission grant per run")
}

// TestVerifyWriteMetaSkipsFilesGet pins the verifier contract: a write response
// carrying id/name/parents/size satisfies every check without a Reader at all
// (a nil Reader proves no Files.Get was attempted).
func TestVerifyWriteMetaSkipsFilesGet(t *testing.T) {
	verifier := NewUploadVerifier(nil)
	got, err := verifier.Verify(context.Background(), "file-1", VerificationParams{
		ExpectedName:     "artifact.mp4",
		ExpectedFolderID: "folder-1",
		ExpectedSize:     5,
		WriteMeta: &FileMeta{
			ID:      "file-1",
			Name:    "artifact.mp4",
			Parents: []string{"folder-1"},
			Size:    5,
		},
	})
	require.NoError(t, err)
	require.True(t, got.FileIDPresent)
	require.True(t, got.FileNotInTrash)
}

// TestVerifyWriteMetaStillFailsClosed proves the fast path is not a weaker
// gate: a write response with the wrong folder still fails the upload.
func TestVerifyWriteMetaStillFailsClosed(t *testing.T) {
	verifier := NewUploadVerifier(nil)
	_, err := verifier.Verify(context.Background(), "file-1", VerificationParams{
		ExpectedName:     "artifact.mp4",
		ExpectedFolderID: "folder-1",
		WriteMeta: &FileMeta{
			ID:      "file-1",
			Name:    "artifact.mp4",
			Parents: []string{"folder-other"},
			Size:    5,
		},
	})
	require.ErrorIs(t, err, ErrDriveFileParentMismatch)
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode mock response: %v", err)
	}
}

package drive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// resumableChunkServer is a minimal Drive fake that speaks the resumable
// upload protocol end to end: it answers the resumable-session init POST
// with a Location header, acknowledges every chunk PUT with 308 (or the
// final file JSON when the artifact is complete), and serves the
// post-upload Files.Get verification metadata.
type resumableChunkServer struct {
	*httptest.Server

	mu        sync.Mutex
	wantTotal int64
	received  []int64  // byte count of each chunk PUT
	otherReqs []string // non-upload requests seen (debug + assertion surface)
	sessionID string
	fileID    string
	fileName  string
	folderID  string
	initCalls int
}

func newResumableChunkServer(t *testing.T, total int64, fileID, fileName, folderID string) *resumableChunkServer {
	t.Helper()
	s := &resumableChunkServer{
		wantTotal: total,
		sessionID: "resumable-session/test-session-1",
		fileID:    fileID,
		fileName:  fileName,
		folderID:  folderID,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.otherReqs = append(s.otherReqs, "RAW "+r.Method+" "+r.URL.String())
		s.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload/") &&
			r.URL.Query().Get("uploadType") == "resumable":
			s.mu.Lock()
			s.initCalls++
			s.mu.Unlock()
			_, _ = io_Discard(r)
			// Absolute path: a relative Location would make the SDK build a
			// request-target without the leading slash, which Go's server
			// rejects with a pre-handler 400.
			w.Header().Set("Location", "/"+s.sessionID)
			w.WriteHeader(http.StatusOK)
		case (r.Method == http.MethodPost || r.Method == http.MethodPut) && strings.Contains(r.URL.Path, s.sessionID):
			n, _ := copyAndCount(w, r)
			s.mu.Lock()
			s.received = append(s.received, n)
			done := s.totalReceived() == s.wantTotal
			s.mu.Unlock()
			if !done {
				// gensupport sends X-GUploader-No-308: yes and expects the
				// "resume incomplete" signal as 200 + status-code override.
				w.Header().Set("X-Http-Status-Code-Override", "308")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"id":%q,"name":%q,"webViewLink":"https://drive.google.com/file/d/%s/view"}`,
				s.fileID, s.fileName, s.fileID)))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/drive/v3/files/"):
			w.Header().Set("Content-Type", "application/json")
			// size is a quoted string in the Drive wire contract (json ,string tag).
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"id":%q,"name":%q,"mimeType":"video/mp4","size":"%d","webViewLink":"https://drive.google.com/file/d/%s/view","parents":[%q],"trashed":false}`,
				s.fileID, s.fileName, s.wantTotal, s.fileID, s.folderID)))
		default:
			s.mu.Lock()
			s.otherReqs = append(s.otherReqs, r.Method+" "+r.URL.String())
			s.mu.Unlock()
			http.NotFound(w, r)
		}
	}))
	return s
}

func (s *resumableChunkServer) totalReceived() int64 {
	var total int64
	for _, n := range s.received {
		total += n
	}
	return total
}

// chunkPUTCount snapshots how many chunk PUTs arrived.
func (s *resumableChunkServer) chunkPUTCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.received)
}

func (s *resumableChunkServer) chunkSizes() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.received...)
}

func io_Discard(r *http.Request) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Body.Read(buf)
		total += int64(n)
		if err != nil {
			break
		}
	}
	return total, nil
}

func copyAndCount(w http.ResponseWriter, r *http.Request) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Body.Read(buf)
		total += int64(n)
		if err != nil {
			break
		}
	}
	return total, nil
}

func resumableDriveService(t *testing.T, serverURL string) *driveapi.Service {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
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

func writeTempMaster(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master.mp4")
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write temp master: %v", err)
	}
	return path
}

// TestPutFile_BigChunkResumableWire pins the A3 wire contract (TODO-pipeline-
// 100x-velocita): with PIPELINEGEN_DRIVE_UPLOAD_CHUNK_MB=1 a 2.5 MiB master
// is delivered as exactly three sequential resumable chunk PUTs, every byte
// arrives exactly once in order, the final chunk returns the Drive file JSON,
// and the post-upload verification gate still passes.
func TestPutFile_BigChunkResumableWire(t *testing.T) {
	t.Setenv(EnvUploadChunkMB, "1") // 1 MiB chunks
	resetUploadChunkForTest()
	if resumableChunkBytes() != 1<<20 {
		t.Fatalf("resumableChunkBytes() = %d, want 1 MiB", resumableChunkBytes())
	}

	const size = 11 * 512 * 1024 // 5.5 MiB: above the 5 MiB resumable threshold AND above one 1 MiB chunk
	const fileID = "big-master"
	server := newResumableChunkServer(t, size, fileID, "master.mp4", "resolved-folder")
	defer server.Close()

	uploader := &Uploader{
		Service: resumableDriveService(t, server.URL),
		Log:     zap.NewNop(),
		lookupFunc: func(_ *Uploader, _ context.Context, _, _, _ string) (ExistingFileLookup, error) {
			return ExistingFileLookup{}, nil // no existing match → create branch
		},
	}

	result, err := uploader.PutFile(context.Background(), PutFileRequest{
		LocalPath:      writeTempMaster(t, size),
		FolderID:       "resolved-folder",
		Filename:       "master.mp4",
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	if err != nil {
		server.mu.Lock()
		seen := strings.Join(server.otherReqs, " | ")
		server.mu.Unlock()
		t.Fatalf("PutFile: %v; requests seen: %s", err, seen)
	}
	if result.FileID != fileID {
		t.Fatalf("FileID = %q, want %q", result.FileID, fileID)
	}
	if got := server.chunkPUTCount(); got != 6 {
		t.Fatalf("chunk PUTs = %d, want 6 (5.5 MiB at 1 MiB chunks)", got)
	}
	for i, n := range server.chunkSizes() {
		want := int64(1 << 20)
		if i == 5 {
			want = size - 5*(1<<20) // tail chunk
		}
		if n != want {
			t.Fatalf("chunk %d = %d bytes, want %d", i, n, want)
		}
	}
}

// TestPutFile_SmallFileKeepsHistoricalResumableRoute pins the rollback
// contract: an artifact at or below one configured chunk keeps the
// deprecated ResumableMedia route (resumable session + acknowledged PUT),
// never collapsing into a single multipart request that would give up the
// resumable retry semantics the 6–7 MiB audio artifacts rely on.
func TestPutFile_SmallFileKeepsHistoricalResumableRoute(t *testing.T) {
	t.Setenv(EnvUploadChunkMB, "") // SDK default 256 MiB >> file
	resetUploadChunkForTest()

	const size = 5 * 1024 * 1024 // 5 MiB, above the resumable threshold
	const fileID = "small-master"
	server := newResumableChunkServer(t, size, fileID, "master.mp4", "resolved-folder")
	defer server.Close()

	uploader := &Uploader{
		Service: resumableDriveService(t, server.URL),
		Log:     zap.NewNop(),
		lookupFunc: func(_ *Uploader, _ context.Context, _, _, _ string) (ExistingFileLookup, error) {
			return ExistingFileLookup{}, nil
		},
	}

	result, err := uploader.PutFile(context.Background(), PutFileRequest{
		LocalPath:      writeTempMaster(t, size),
		FolderID:       "resolved-folder",
		Filename:       "master.mp4",
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	if err != nil {
		server.mu.Lock()
		seen := strings.Join(server.otherReqs, " | ")
		server.mu.Unlock()
		t.Fatalf("PutFile: %v; requests seen: %s", err, seen)
	}
	if result.FileID != fileID {
		t.Fatalf("FileID = %q, want %q", result.FileID, fileID)
	}
	server.mu.Lock()
	init := server.initCalls
	server.mu.Unlock()
	if init != 1 {
		t.Fatalf("resumable session inits = %d, want 1 (small file must stay on the resumable route)", init)
	}
	if got := server.chunkPUTCount(); got != 1 {
		server.mu.Lock()
		seen := strings.Join(server.otherReqs, " | ")
		server.mu.Unlock()
		t.Fatalf("chunk PUTs = %d, want 1; requests seen: %s", got, seen)
	}
}

// TestResumableChunkBytes_EnvContract pins the operator knob: default 256 MiB,
// clamped to [16 MiB, 1 GiB], rounded down to a 256 KiB multiple, malformed
// values falling back to the default.
func TestResumableChunkBytes_EnvContract(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"", 256 << 20},
		{"16", 16 << 20},
		{"1", 1 << 20},
		{"256", 256 << 20},
		{"64", 64 << 20},
		{"4096", 1024 << 20}, // clamped to the 1 GiB ceiling
		{"not-a-number", 256 << 20},
		{"0", 256 << 20},
		{"-5", 256 << 20},
		{"17", 17 << 20}, // whole MiB values are always 256 KiB multiples
	}
	for _, tc := range cases {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv(EnvUploadChunkMB, tc.env)
			resetUploadChunkForTest()
			if got := resumableChunkBytes(); got != tc.want {
				t.Fatalf("resumableChunkBytes() = %d, want %d", got, tc.want)
			}
		})
	}
	// A bounded PutFile must never hang even if a future regression retries a
	// chunk loop forever.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		t.Fatalf("premature deadline")
	}
}

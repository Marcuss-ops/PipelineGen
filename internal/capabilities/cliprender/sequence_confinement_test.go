package cliprender

// sequence_confinement_test.go — ticket CR-19-04: the sequence contract must not
// turn an HTTP body into a filesystem address.
//
// The standalone route and the batch's embedded sequence both used to accept
// `clips[].path` and `output.dir` verbatim: the worker read the file and wrote
// the merge there under the service account. These tests pin the edge gate and
// the worker-side confinement of the output directory.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func assertBadPayload(t *testing.T, path, body string, svc *stubJobsSvc) {
	t.Helper()
	router := newTestRouter(svc)
	req := httptest.NewRequest("POST", path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%s: status = %d, want 400; body=%s", path, rec.Code, rec.Body.String())
	}
	var resp renderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode response: %v", path, err)
	}
	if resp.ErrorCode != ErrCodeInvalidPayload {
		t.Fatalf("%s: error_code = %q, want %q", path, resp.ErrorCode, ErrCodeInvalidPayload)
	}
	if svc.enqueued != nil {
		t.Fatalf("%s: a rejected sequence must not enqueue anything", path)
	}
}

func TestSequenceEdgeRejectsLocalFilesystemInputs(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "clip path",
			path: "/render/sequence",
			body: `{"clips":[{"path":"/etc/passwd","duration_ms":1000}]}`,
		},
		{
			name: "output dir",
			path: "/render/sequence",
			body: `{"clips":[{"asset_id":"asset-a"}],"output":{"dir":"/tmp/escape"}}`,
		},
		{
			name: "batch output dir",
			path: "/render/batch",
			body: `{"items":[{"source_asset_id":"asset-a"}],"sequence":{"output":{"dir":"/tmp/escape"}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertBadPayload(t, tc.path, tc.body, &stubJobsSvc{})
		})
	}
}

func TestSequenceEdgeStillAcceptsAssetAndJobSources(t *testing.T) {
	svc := &stubJobsSvc{returnJob: &job.Job{ID: "job-seq"}}
	router := newTestRouter(svc)
	body := `{"clips":[{"asset_id":"asset-a"},{"job_id":"job-b"}]}`
	req := httptest.NewRequest("POST", "/render/sequence", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if svc.enqueued == nil {
		t.Fatal("a valid sequence must enqueue the assembly job")
	}
}

// TestSequenceOutputDirIsConfinedToRoot is the worker-side half: even a job
// payload produced out of band cannot choose a directory outside the wired
// sequence root.
func TestSequenceOutputDirIsConfinedToRoot(t *testing.T) {
	root := t.TempDir()
	assembly := SequenceAssembly{Root: root}

	if got, err := assembly.OutputDir("", "job-1"); err != nil {
		t.Fatalf("default output dir: %v", err)
	} else if got != filepath.Join(root, "job-job-1") {
		t.Fatalf("default output dir = %q, want %q", got, filepath.Join(root, "job-job-1"))
	}

	inside := filepath.Join(root, "inside")
	if got, err := assembly.OutputDir(inside, "job-2"); err != nil {
		t.Fatalf("absolute path inside the root: %v", err)
	} else if got != inside {
		t.Fatalf("absolute inside root = %q, want %q", got, inside)
	}

	if got, err := assembly.OutputDir("relative", "job-3"); err != nil {
		t.Fatalf("relative path: %v", err)
	} else if got != filepath.Join(root, "relative") {
		t.Fatalf("relative path = %q, want it resolved under the root", got)
	}

	for _, escape := range []string{"../escape", "a/../../escape", "/etc", filepath.Join(root, "..", "escape")} {
		if _, err := assembly.OutputDir(escape, "job-4"); err == nil {
			t.Fatalf("output dir %q escaped the root unnoticed", escape)
		} else if !strings.Contains(err.Error(), "escapes the assembly root") {
			t.Fatalf("output dir %q: error = %v, want the typed escape error", escape, err)
		}
	}
}

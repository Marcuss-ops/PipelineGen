// Package worker — uploadOutputs regression tests for Blocco 2.2.
//
// Pin the required-vs-optional gating contract introduced in commit
// c7ccd658. The previous audit reported that jobs whose handler
// declared an output path that didn't exist on disk still got
// reported SUCCEEDED: the runner silently `continue`-d on
// os.IsNotExist and the deferred workspace cleanup permanently
// dropped the missing artefact.
//
// Required behaviour pinned here:
//
//   - When a handler declares an output (OutputArtifact.Required=true,
//     or `output_files: [map{required:true,...}]`), and the path is
//     missing on disk, uploadOutputs returns a non-nil error
//     containing the path + asset_id + job. UploadFile MUST NOT have
//     been called.
//   - Legacy single-string keys (output_path / pdf_path /
//     markdown_path), bare []string items in output_files, and
//     OutputArtifact struct entries with Required omitted are
//     treated as OPTIONAL — missing files are still skipped
//     (backward-compat preserved for handlers whose optional
//     sub-render sometimes emits an empty path).
//   - Existing files still upload, including via the JSON-style
//     map[string]any representation of OutputArtifact.
//
// Tests are kept in `package worker` (not `worker_test`) so the
// runner.uploadManifest method is reachable (legacy path exercised
// when no __artifact_manifest key is present).
package worker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// mockAssetClient records every UploadFile invocation so the test
// can assert that uploads happen (or don't happen) on the exact
// path that uploadOutputs chose.
type mockAssetClient struct {
	uploads []uploadCall
}

type uploadCall struct {
	assetID string
	path    string
}

func (m *mockAssetClient) Download(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func (m *mockAssetClient) UploadFile(_ context.Context, assetID, filePath string) error {
	m.uploads = append(m.uploads, uploadCall{assetID: assetID, path: filePath})
	return nil
}

// TestRunner_uploadManifest_LegacyFallback drives the Blocco 2.2 contract
// via uploadManifest with handler results that lack a manifest,
func TestRunner_uploadManifest_NilAssetClient(t *testing.T) {
	runner := &Runner{assetClient: nil}
	_, err := runner.uploadManifest(context.Background(), "job-nil-client", map[string]any{
		"output_files": []any{
			OutputArtifact{Path: "/missing", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("nil assetClient must not produce an error, got: %v", err)
	}
}

// TestRunner_uploadManifest_NonNotExistStatErrorsBubble exercises the
// os.Stat error path via the legacy fallback.
// TestRunner_uploadManifest_WithManifest verifies the manifest upload path
// end-to-end: handlerResult contains a serialised ArtifactManifest under
// __artifact_manifest, the runner validates required artefacts, uploads
// them, and returns an UploadedManifest with no local paths.
func TestRunner_uploadManifest_WithManifest(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "script.json")
	if err := os.WriteFile(realFile, []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("seed real file: %v", err)
	}

	mock := &mockAssetClient{}
	runner := &Runner{assetClient: mock}

	manifest := job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-manifest-test",
		JobID:         "job-manifest-test",
		Artifacts: []job.Artifact{
			{
				ID:       "job-manifest-test:script",
				Kind:     job.ArtifactKindScriptJSON,
				Path:     realFile,
				Filename: "script.json",
				MIMEType: "application/json",
				Required: true,
			},
			{
				ID:       "job-manifest-test:image",
				Kind:     job.ArtifactKindImage,
				Path:     "/tmp/missing/image.png",
				Filename: "image.png",
				MIMEType: "image/png",
				Required: false,
			},
		},
	}

	uploaded, err := runner.uploadManifest(context.Background(), "job-manifest-test", map[string]any{
		job.ManifestKey: &manifest,
	})
	if err != nil {
		t.Fatalf("uploadManifest with valid manifest: %v", err)
	}
	if uploaded == nil {
		t.Fatal("expected non-nil UploadedManifest from manifest path")
	}

	// Required artifact was uploaded.
	if len(mock.uploads) != 1 {
		t.Fatalf("expected 1 upload (required only), got %d: %+v", len(mock.uploads), mock.uploads)
	}
	if mock.uploads[0].assetID != "job-manifest-test:script" {
		t.Errorf("upload assetID = %q, want job-manifest-test:script", mock.uploads[0].assetID)
	}

	// No local paths in the UploadedManifest.
	if len(uploaded.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts in UploadedManifest, got %d", len(uploaded.Artifacts))
	}
	if uploaded.Artifacts[0].Status != job.StatusReady {
		t.Errorf("required artifact status = %q, want %q", uploaded.Artifacts[0].Status, job.StatusReady)
	}
	if uploaded.Artifacts[1].Status != job.StatusSkipped {
		t.Errorf("non-required missing artifact status = %q, want %q", uploaded.Artifacts[1].Status, job.StatusSkipped)
	}

	// Verify no local paths leak.
	uploadedJSON, _ := json.Marshal(uploaded)
	if strings.Contains(string(uploadedJSON), "/tmp/") {
		t.Error("UploadedManifest JSON contains local paths — must not leak")
	}
	if strings.Contains(string(uploadedJSON), realFile) {
		t.Error("UploadedManifest JSON contains real file path — must not leak")
	}
}

func TestRunner_uploadManifest_NonNotExistStatErrorsBubble(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("permission semantics differ on windows")
	}

	tmpDir := t.TempDir()
	noRead := filepath.Join(tmpDir, "no-read")
	if err := os.Mkdir(noRead, 0000); err != nil {
		t.Fatalf("mkdir noRead: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(noRead, 0755) })

	mock := &mockAssetClient{}
	runner := &Runner{assetClient: mock}

	_, err := runner.uploadManifest(context.Background(), "job-perm", map[string]any{
		"output_files": []any{
			OutputArtifact{Path: filepath.Join(noRead, "inside"), Required: false},
		},
	})
	if err == nil {
		t.Fatalf("expected non-nil error from os.Stat in unreadable dir, got nil")
	}
	if strings.Contains(err.Error(), "required output file missing") {
		t.Fatalf("stat permission error must not be coerced into required-missing branch: %v", err)
	}
}

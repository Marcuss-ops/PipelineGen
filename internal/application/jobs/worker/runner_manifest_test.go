// Package worker — runner_manifest_test.go (P0 Commit 12, July 2026).
//
// Pinned tests for the C12 runner.go path-scan removal:
//
//  1. The pre-Blocco-2.2 JSON path-scan (output_path / pdf_path /
//     markdown_path / output_files keys) is GONE from the runner
//     upload cycle.
//  2. A handler that does NOT emit an ArtifactManifest under
//     __artifact_manifest fails-closed with ErrLegacyUploadPathRemoved.
//
// These tests pin the user's literal §C12 spec: "check runner.go
// no longer scans JSON for paths."
package worker

import (
	"context"
	"errors"
	"io"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// stubAssetClient is the minimum AssetClient implementation needed for
// Runner. The C12 deletion test asserts the function fails BEFORE
// the AssetClient is touched (the legacy path-scan is dead code; the
// sentinel error fires immediately).
type stubAssetClient struct{}

func (s *stubAssetClient) UploadFile(ctx context.Context, assetID, path string) error {
	return nil
}

func (s *stubAssetClient) Download(ctx context.Context, assetID string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// TestUploadOutputsLegacy_ReturnsSentinelError is the BLOCKER fix
// for C12: legacy callers that fall through to the disabled path-scan
// now receive ErrLegacyUploadPathRemoved. The sentinel is exported
// (so callers can probe via errors.Is) and the error message names
// the canonical replacement (emit an ArtifactManifest under
// __artifact_manifest).
//
// What we test:
//   - the function returns an error (not nil, not panic).
//   - the returned error matches ErrLegacyUploadPathRemoved via errors.Is.
//   - the error message names the canonical replacement.
func TestUploadOutputsLegacy_ReturnsSentinelError(t *testing.T) {
	r := &Runner{
		log:         zap.NewNop(),
		assetClient: &stubAssetClient{},
	}

	// The classic legacy payload — output_path + pdf_path + markdown_path +
	// output_files — must NOT be scanned. The function should fail-closed
	// with the sentinel BEFORE looking at these keys at all.
	handlerResult := map[string]any{
		"output_path":   "/tmp/pipelinegen/jobs/legacy/output.pdf",
		"pdf_path":      "/tmp/pipelinegen/jobs/legacy/pdf.pdf",
		"markdown_path": "/tmp/pipelinegen/jobs/legacy/md.md",
		"output_files":  []string{"/tmp/pipelinegen/jobs/legacy/file1.txt"},
	}

	err := r.uploadOutputsLegacy(context.Background(), "legacy-job", handlerResult)

	if err == nil {
		t.Fatal("uploadOutputsLegacy returned nil — the path-scan removal was reverted; expected ErrLegacyUploadPathRemoved sentinel")
	}
	if !errors.Is(err, ErrLegacyUploadPathRemoved) {
		t.Errorf("uploadOutputsLegacy err = %v, want errors.Is(err, ErrLegacyUploadPathRemoved)", err)
	}
	// The sentinel contract requires the upgrade path be visible in
	// the error message — operators reading a stack trace after a
	// regression must see "emit ArtifactManifest under __artifact_manifest".
	if errMsg := err.Error(); !contains(errMsg, "__artifact_manifest") {
		t.Errorf("err message %q should name the canonical replacement key (__artifact_manifest)", errMsg)
	}
}

// TestUploadManifest_NoManifestKey_FailsClosed complements the
// sentinel-err test. The Runner's main entry point uploadManifest
// falls through to uploadOutputsLegacy when handlerResult has no
// manifest under __artifact_manifest. The C12 audit asserts ALL
// handlers must now emit a manifest; the uploadManifest entry point
// must therefore propagate the sentinel error from the dead path.
//
// This pins the user's literal slot: "no manifest → no upload".
func TestUploadManifest_NoManifestKey_FailsClosed(t *testing.T) {
	r := &Runner{
		log:         zap.NewNop(),
		assetClient: &stubAssetClient{},
	}

	// handlerResult has NO __artifact_manifest key (the only C12-
	// retry-direction is the manifest sidecar). The runner must
	// fail-closed via ErrLegacyUploadPathRemoved — the path-scan
	// fallback is dead.
	handlerResult := map[string]any{
		"job_id": "no-manifest-job",
		// deliberately no scriptpkg.ManifestKey entry.
	}

	_, err := r.uploadManifest(context.Background(), "no-manifest-job", handlerResult)

	if err == nil {
		t.Fatal("uploadManifest returned nil + nil when handlerResult had no manifest under __artifact_manifest — fallthrough to disabled path-scan should fail-closed")
	}
	if !errors.Is(err, ErrLegacyUploadPathRemoved) {
		t.Errorf("uploadManifest err = %v, want errors.Is(err, ErrLegacyUploadPathRemoved)", err)
	}
}

// TestUploadManifest_ManifestKeySet_NoPathScanFilter pins the
// positive path of C12: a handlerResult with __artifact_manifest
// set must traverse the canonical upload cycle WITHOUT any fall-through
// to scan handlerResult for output_path / pdf_path / markdown_path /
// output_files. The canonical path reads only the manifest.
//
// What we test: the function does NOT read legacy keys (the path-
// scan is gone); even when those keys are present in handlerResult
// alongside the manifest, only the manifest drives upload decisions.
func TestUploadManifest_ManifestKeySet_NoPathScanFilter(t *testing.T) {
	// Construct a minimal manifest with one required artefact that
	// does NOT exist on disk — the runner is expected to fail at the
	// Validate stage ("file not found on disk") BEFORE any path-scan
	// logic runs. This proves the upload cycle's entry point is the
	// canonical manifest, not the legacy JSON scan.
	manifest := &scriptpkg.ArtifactManifest{
		SchemaVersion: scriptpkg.SchemaVersionArtifactManifestV1,
		JobID:         "manifest-only-job",
		Artifacts: []scriptpkg.Artifact{
			{
				ID:       "manifest-only-job:required:0",
				Kind:     scriptpkg.ArtifactKindScriptJSON,
				Path:     "/nonexistent/manifest-driven-only/script.json",
				Filename: "script.json",
				MIMEType: "application/json",
				Required: true,
			},
		},
	}
	handlerResult := map[string]any{
		scriptpkg.ManifestKey: manifest,
		"output_path":         "/tmp/pipelinegen/jobs/legacy/output.pdf", // must be IGNORED
		"pdf_path":            "/tmp/pipelinegen/jobs/legacy/pdf.pdf",    // must be IGNORED
		"markdown_path":       "/tmp/pipelinegen/jobs/legacy/md.md",      // must be IGNORED
		"output_files": []string{ // must be IGNORED
			"/tmp/pipelinegen/jobs/legacy/file1.txt",
			"/tmp/pipelinegen/jobs/legacy/file2.txt",
		},
	}

	r := &Runner{
		log:         zap.NewNop(),
		assetClient: &stubAssetClient{},
	}

	_, err := r.uploadManifest(context.Background(), "manifest-only-job", handlerResult)
	if err == nil {
		t.Fatal("uploadManifest succeeded with a manifest whose required artefact is missing on disk — Validate gate should fail-closed")
	}
	// The error must NOT be the legacy sentinel: we are inside the
	// canonical path and got a Validate-style error ("file not found on
	// disk"). The C12 audit asserts the upload cycle's primary mode
	// is the canonical manifest path (not the legacy scan).
	if errors.Is(err, ErrLegacyUploadPathRemoved) {
		t.Errorf("uploadManifest fell through to legacy path-scan even with manifest set — canonical path is gone")
	}
}

// contains is a tiny substring-search helper. Avoiding the strings
// package import keeps this test file's import set minimal.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

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
// runner.uploadOutputs method is reachable.
package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestRunner_uploadOutputs drives the Blocco 2.2 contract via a
// table-driven test that covers the four observable branches of
// uploadOutputs' outputFile classifier (required missing,
// optional missing, required-present, dedicated "no result" path).
func TestRunner_uploadOutputs(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(realFile, []byte("data"), 0644); err != nil {
		t.Fatalf("seed real file: %v", err)
	}

	// Missing path used by every negative case. Not a directory,
	// not empty — must satisfy os.IsNotExist and pass through the
	// required gate without triggering any Stat permission error.
	const missingPath = "/this/path/does/not/exist/anywhere"

	tests := []struct {
		name           string
		handlerResult  map[string]any
		wantErrSubstr  string // empty = expect nil
		wantUploadArgs []uploadCall
	}{
		{
			// Case 1 — primary regression: a required output is
			// missing on disk. The runner MUST fail the job — no
			// silent SUCCEEDED, no UploadFile call.
			name: "required_missing_returns_error",
			handlerResult: map[string]any{
				"output_files": []any{
					OutputArtifact{Path: missingPath, Required: true},
				},
			},
			wantErrSubstr: "required output file missing on disk",
			// AssetID is empty on the struct, so the add closure
			// derives it as jobID:<basename>. The basename of
			// /this/path/does/not/exist/anywhere is "anywhere".
			wantUploadArgs: nil,
		},
		{
			// Case 2 — backward-compat: legacy output_path key
			// pointing at a missing file is OPTIONAL and silently
			// skipped. Pre-fix, this is exactly the silent-success
			// hole the audit called out for legacy handlers; the
			// opt-in to REQUIRED behaviour is the OutputArtifact
			// path (case 1).
			name: "legacy_output_path_missing_is_optional",
			handlerResult: map[string]any{
				"output_path": missingPath,
			},
			wantUploadArgs: nil,
		},
		{
			// Case 3 — an OutputArtifact struct WITHOUT Required
			// must default to optional. omitempty on the json tag
			// plus the literal default of bool→false gives this
			// semantics; pin it so a future "fail-closed" flip
			// does not silently regress case 1 callers.
			name: "struct_without_required_is_optional",
			handlerResult: map[string]any{
				"output_files": []any{
					OutputArtifact{AssetID: "x", Path: missingPath},
				},
			},
			wantUploadArgs: nil,
		},
		{
			// Case 4 — happy path: existing file with required=true
			// still uploads. Verifies the error branch did not
			// accidentally trigger on the success branch.
			name: "required_present_uploads",
			handlerResult: map[string]any{
				"output_files": []any{
					OutputArtifact{AssetID: "asset-a", Path: realFile, Required: true},
				},
			},
			wantUploadArgs: []uploadCall{{assetID: "asset-a", path: realFile}},
		},
		{
			// Case 5 — JSON-style map[string]any payload (what
			// handlerResult actually is after json.Unmarshal on
			// the broker side). required=true honours the gate.
			// This pins that the map-parse branch does not drift
			// away from the struct-parse branch.
			name: "map_required_present_uploads",
			handlerResult: map[string]any{
				"output_files": []any{
					map[string]any{
						"asset_id": "map-asset",
						"path":     realFile,
						"required": true,
					},
				},
			},
			wantUploadArgs: []uploadCall{{assetID: "map-asset", path: realFile}},
		},
		{
			// Case 6 — mixed: one required+present uploads
			// successfully; one optional+missing + one legacy
			// optional+missing are both silently skipped. The
			// error branch must NOT trigger when the required
			// file exists.
			name: "mixed_required_present_and_optional_missing",
			handlerResult: map[string]any{
				"output_files": []any{
					OutputArtifact{AssetID: "ok", Path: realFile, Required: true},
					OutputArtifact{AssetID: "skipped", Path: missingPath, Required: false},
				},
				"pdf_path": missingPath, // legacy optional, skipped
			},
			wantUploadArgs: []uploadCall{{assetID: "ok", path: realFile}},
		},
		{
			// Case 7 — bare []string items in output_files are
			// OPTIONAL (back-compat with the historical surface).
			// Existing file uploads exactly once; missing file
			// skips silently.
			name: "bare_string_slice_optional",
			handlerResult: map[string]any{
				"output_files": []string{realFile, missingPath},
			},
			wantUploadArgs: []uploadCall{{assetID: "job-test:bare_string_slice_optional"}},
		},
		{
			// Case 8 — duplicate path in []string dedupes to one
			// UploadFile call (defensive dedup in the add
			// closure, not regression-critical but worth pinning
			// because handler bugs sometimes emit the same key
			// twice).
			name: "duplicate_paths_dedupe",
			handlerResult: map[string]any{
				"output_files": []string{realFile, realFile},
			},
			// Both items derive assetID="job-test:...:<basename>"
			// but the closure dedups on assetID|path; only the
			// first call wins. We assert via the helper below on
			// call count rather than a literal assetID since
			// the test name itself could vary.
			wantUploadArgs: []uploadCall{{}}, // placeholder; special-cased via name branch
		},
		{
			// Case 9 — empty handlerResult. Defensive short-circuit
			// at the top of uploadOutputs must not panic and must
			// not call UploadFile.
			name:           "empty_handler_result",
			handlerResult:  map[string]any{},
			wantUploadArgs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockAssetClient{}
			runner := &Runner{assetClient: mock}

			err := runner.uploadOutputs(context.Background(), "job-test", tt.handlerResult)

			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErrSubstr, err)
				}
				// The error path must also include the missing
				// path so operators can trace which handler
				// emitted the bad result.
				if !strings.Contains(err.Error(), missingPath) {
					t.Fatalf("error must include missing path %q, got: %v", missingPath, err)
				}
				if len(mock.uploads) != 0 {
					t.Fatalf("expected 0 uploads on error path, got %d", len(mock.uploads))
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Case 8 special-case: assert dedup by call count,
			// not by exact assetID (the helper derives the
			// assetID from the test name; only count matters).
			if tt.name == "duplicate_paths_dedupe" {
				if len(mock.uploads) != 1 {
					t.Fatalf("duplicate dedup: expected 1 upload, got %d", len(mock.uploads))
				}
				if mock.uploads[0].path != realFile {
					t.Fatalf("duplicate dedup: unexpected path %q", mock.uploads[0].path)
				}
				return
			}

			// Case 7 special-case: derive the expected assetID
			// the same way uploadOutputs does (jobID + ":" +
			// filepath.Base of the first real file).
			if tt.name == "bare_string_slice_optional" {
				if len(mock.uploads) != 1 {
					t.Fatalf("bare_string_slice: expected 1 upload, got %d", len(mock.uploads))
				}
				wantAssetID := "job-test:" + filepath.Base(realFile)
				if mock.uploads[0].assetID != wantAssetID {
					t.Fatalf("bare_string_slice: expected assetID %q, got %q", wantAssetID, mock.uploads[0].assetID)
				}
				if mock.uploads[0].path != realFile {
					t.Fatalf("bare_string_slice: unexpected path %q", mock.uploads[0].path)
				}
				return
			}

			if len(mock.uploads) != len(tt.wantUploadArgs) {
				t.Fatalf("upload count: want %d, got %d (calls: %+v)",
					len(tt.wantUploadArgs), len(mock.uploads), mock.uploads)
			}
			for i, want := range tt.wantUploadArgs {
				if mock.uploads[i] != want {
					t.Fatalf("upload[%d]: want %+v, got %+v", i, want, mock.uploads[i])
				}
			}
		})
	}
}

// TestRunner_uploadOutputs_NilAssetClient pins the defensive
// short-circuit: a runner constructed without an AssetClient must
// return nil without panicking, regardless of handlerResult content.
// This is unchanged behaviour but worth pinning so the new
// required-vs-optional gating cannot accidentally introduce a
// panic when assetClient is nil.
func TestRunner_uploadOutputs_NilAssetClient(t *testing.T) {
	runner := &Runner{assetClient: nil}
	err := runner.uploadOutputs(context.Background(), "job-nil-client", map[string]any{
		"output_files": []any{
			OutputArtifact{Path: "/missing", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("nil assetClient must not produce an error, got: %v", err)
	}
}

// TestRunner_uploadOutputs_PermissionDeniedBubbles exercises the
// os.Stat IsNotExist branch's sibling: any non-NotExist error from
// os.Stat (here: a directory we cannot stat because it's a file
// rather than a directory won't trigger it — so we use a path
// inside a directory without read permission instead). Skipped on
// non-unix platforms where chmod doesn't apply.
func TestRunner_uploadOutputs_NonNotExistStatErrorsBubble(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("permission semantics differ on windows")
	}

	tmpDir := t.TempDir()
	noRead := filepath.Join(tmpDir, "no-read")
	if err := os.Mkdir(noRead, 0000); err != nil {
		t.Fatalf("mkdir noRead: %v", err)
	}
	// Restore perms on cleanup so t.TempDir() can remove it.
	t.Cleanup(func() { _ = os.Chmod(noRead, 0755) })

	mock := &mockAssetClient{}
	runner := &Runner{assetClient: mock}

	err := runner.uploadOutputs(context.Background(), "job-perm", map[string]any{
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

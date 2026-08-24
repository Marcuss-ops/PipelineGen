// Package drive — file_lifecycle_test.go (CARD-3, June 2026)
//
// Tests for *FileLifecycleAdapter. Focus on input-validation paths
// (which are unique per method and cheap to assert without an
// httptest server); happy-path Drive flows are exercised by integration
// tests against a real Drive sandbox (gated by VELOX_INTEGRATION_DRIVE_TESTS).
//
// Each test pins ONE method's input-validation contract so surface
// drift between the FileLifecycle port and *FileLifecycleAdapter
// surfaces as a test failure distinct from the compile-time assert
// at the bottom of the file.
package drive

import (
	"context"
	"strings"
	"testing"
	"time"

	gdrive "google.golang.org/api/drive/v3"
)

// TestFileLifecycleAdapter_Trash_RequiresFileID pins the Trash early-
// rejection branch: empty fileID MUST return an error with prefix
// "trash: file id is required" so callers (e.g. the semantic_enricher
// metadata.json renewal path) can branch on intent without an
// httptest setup.
func TestFileLifecycleAdapter_Trash_RequiresFileID(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	err := a.Trash(context.Background(), "")
	if err == nil {
		t.Fatal("Trash(empty fileID) should reject")
	}
	if !strings.Contains(err.Error(), "trash: file id is required") {
		t.Errorf("Trash: unexpected error: %v", err)
	}
}

// TestFileLifecycleAdapter_Delete_RequiresFileID pins the Delete
// early-rejection branch (Wave C preparation, June 2026). Mirrors
// the Trash test pattern above; the error prefix is "delete: file id
// is required". Delete is the C1 file-lifecycle reallocation target
// for the pre-Wave-C drive.Admin.DeleteFile method.
func TestFileLifecycleAdapter_Delete_RequiresFileID(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	err := a.Delete(context.Background(), "")
	if err == nil {
		t.Fatal("Delete(empty fileID) should reject")
	}
	if !strings.Contains(err.Error(), "delete: file id is required") {
		t.Errorf("Delete: unexpected error: %v", err)
	}
}

// TestFileLifecycleAdapter_AddParent_RequiresFileIDAndParent pins both
// AddParent validation branches in one test: empty fileID AND empty
// newParentID. Wave D (June 2026) D1: renamed from Move to AddParent
// to match the actual multi-parent-add semantics; the validation
// contract is unchanged.
func TestFileLifecycleAdapter_AddParent_RequiresFileIDAndParent(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	if err := a.AddParent(context.Background(), "", "p"); err == nil {
		t.Error("AddParent(empty fileID) should reject")
	}
	if err := a.AddParent(context.Background(), "f", ""); err == nil {
		t.Error("AddParent(empty newParentID) should reject")
	}
}

// TestFileLifecycleAdapter_Rename_RequiresFileIDAndName pins both Rename
// validation branches in one test: empty fileID AND empty newName.
func TestFileLifecycleAdapter_Rename_RequiresFileIDAndName(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	if err := a.Rename(context.Background(), "", "n"); err == nil {
		t.Error("Rename(empty fileID) should reject")
	}
	if err := a.Rename(context.Background(), "f", ""); err == nil {
		t.Error("Rename(empty newName) should reject")
	}
}

// TestFileLifecycleAdapter_Cleanup_RequiresAtLeastOneFilter pins
// Cleanup's at-least-one-filter safety guard (Wave D D2, June 2026).
// A request with all-zero fields would match every non-trashed file
// on Drive (a Drive-wide wipe) and is rejected upfront. The legacy
// TestFileLifecycleAdapter_Cleanup_RequiresQuery was renamed +
// re-shaped to the new CleanupRequest contract.
func TestFileLifecycleAdapter_Cleanup_RequiresAtLeastOneFilter(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	_, err := a.Cleanup(context.Background(), CleanupRequest{})
	if err == nil {
		t.Fatal("Cleanup(empty request) should reject — at least one filter is required")
	}
	if !strings.Contains(err.Error(), "at least one filter is required") {
		t.Errorf("Cleanup: unexpected error: %v", err)
	}
}

// TestFileLifecycleAdapter_Cleanup_AtLeastOneFilter_ReturnsEmptyResult
// pins the early-rejection behavior (Wave D D3, June 2026): when the
// at-least-one-filter guard fires, the returned CleanupResult is the
// zero value (Matched=0, Trashed=0, Failed=0, FailedIDs=[]).
// FailedIDs is initialised to an empty slice (NOT nil) so JSON
// marshalling produces "failed_ids": [] rather than "failed_ids":
// null — a small but operationally-meaningful detail for API
// consumers that branch on the slice's length or pass it to other
// serialisers.
func TestFileLifecycleAdapter_Cleanup_AtLeastOneFilter_ReturnsEmptyResult(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	res, err := a.Cleanup(context.Background(), CleanupRequest{})
	if err == nil {
		t.Fatal("Cleanup(empty request) should reject")
	}
	if res.Matched != 0 || res.Trashed != 0 || res.Failed != 0 {
		t.Errorf("Cleanup(empty request) counts should be zero, got: %+v", res)
	}
	if res.FailedIDs == nil {
		t.Errorf("Cleanup(empty request) FailedIDs should be empty slice (not nil) for JSON correctness, got: %v", res.FailedIDs)
	}
	if len(res.FailedIDs) != 0 {
		t.Errorf("Cleanup(empty request) FailedIDs should be empty, got: %v", res.FailedIDs)
	}
}

// TestCleanupRequest_BuildQuery_AllFieldsCombines pins the Drive
// query construction (Wave D D2, June 2026): all 4 fields set → the
// query contains "trashed = false" + all 4 filter parts joined by
// " and ". The RFC3339 UTC format for OlderThan is asserted
// explicitly so future timezone-handling drift surfaces in a test
// failure rather than silently producing a Drive-wide-wipe query.
func TestCleanupRequest_BuildQuery_AllFieldsCombines(t *testing.T) {
	req := CleanupRequest{
		ParentFolderID: "folder123",
		Name:           "test.mp4",
		MimeType:       "video/mp4",
		OlderThan:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	q, err := req.buildQuery()
	if err != nil {
		t.Fatalf("buildQuery: unexpected error: %v", err)
	}
	if !strings.Contains(q, "trashed = false") {
		t.Errorf("buildQuery: missing trashed filter: %q", q)
	}
	if !strings.Contains(q, "'folder123' in parents") {
		t.Errorf("buildQuery: missing parent filter: %q", q)
	}
	if !strings.Contains(q, "name = 'test.mp4'") {
		t.Errorf("buildQuery: missing name filter: %q", q)
	}
	if !strings.Contains(q, "mimeType = 'video/mp4'") {
		t.Errorf("buildQuery: missing mimeType filter: %q", q)
	}
	if !strings.Contains(q, "modifiedTime < '2026-01-01T00:00:00Z'") {
		t.Errorf("buildQuery: missing OlderThan filter (RFC3339 UTC): %q", q)
	}
}

// Compile-time reinforcement: *FileLifecycleAdapter must implement
// FileLifecycle (mirror of the assert at the bottom of file_lifecycle.go).
var _ FileLifecycle = (*FileLifecycleAdapter)(nil)

// _ = (any)(nil) keeps the gdrive import live when only early-rejection
// tests are wired (e.g. when happy-path Drive round-trip tests are
// gated behind an integration build tag in a follow-up).
var _ *gdrive.Service = nil

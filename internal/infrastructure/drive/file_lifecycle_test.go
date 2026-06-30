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

// TestFileLifecycleAdapter_Cleanup_RequiresQuery pins Cleanup's required-
// query validation (no point in paginating on an empty Drive search).
func TestFileLifecycleAdapter_Cleanup_RequiresQuery(t *testing.T) {
	a := NewFileLifecycleAdapter(nil, nil)
	if _, err := a.Cleanup(context.Background(), ""); err == nil {
		t.Error("Cleanup(empty query) should reject")
	}
}

// Compile-time reinforcement: *FileLifecycleAdapter must implement
// FileLifecycle (mirror of the assert at the bottom of file_lifecycle.go).
var _ FileLifecycle = (*FileLifecycleAdapter)(nil)

// _ = (any)(nil) keeps the gdrive import live when only early-rejection
// tests are wired (e.g. when happy-path Drive round-trip tests are
// gated behind an integration build tag in a follow-up).
var _ *gdrive.Service = nil

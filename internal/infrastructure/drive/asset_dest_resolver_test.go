// Package drive — asset_dest_resolver_test.go pins the reconnected
// asset.Resolver → drive.EnsureFolderPath get-or-create contract and the
// godlike/07 typed-error fail-closed boundary.
package drive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeAdmin is a minimal drive.Admin test double that records the last
// GetOrCreateFolder invocation.
type fakeAdmin struct {
	getOrCreateCalls int
	lastName         string
	lastParentID     string
	cannedID         string
	getOrCreateErr   error
}

func (f *fakeAdmin) GetOrCreateFolder(_ context.Context, name, parentID string) (string, error) {
	f.getOrCreateCalls++
	f.lastName = name
	f.lastParentID = parentID
	return f.cannedID, f.getOrCreateErr
}

// The remaining Admin methods are ctor/stub surfaces not exercised by the
// resolver; they satisfy the interface contract.
func (f *fakeAdmin) GetFolderName(context.Context, string) (string, error) { return "", nil }
func (f *fakeAdmin) TrashFolder(context.Context, string) error             { return nil }
func (f *fakeAdmin) DeleteFolder(context.Context, string) error            { return nil }
func (f *fakeAdmin) TrashFile(context.Context, string) error               { return nil }
func (f *fakeAdmin) DeleteFile(context.Context, string) error              { return nil }
func (f *fakeAdmin) MoveFile(context.Context, string, string, string) error {
	return nil
}
func (f *fakeAdmin) RenameFile(context.Context, string, string) error { return nil }
func (f *fakeAdmin) Ping(context.Context) error                       { return nil }

var _ Admin = (*fakeAdmin)(nil)

func TestAssetDestResolver_CreateSubfolder_UsesGetOrCreate(t *testing.T) {
	admin := &fakeAdmin{cannedID: "child-folder-id"}
	r := NewAssetDestResolver(admin)
	if r == nil {
		t.Fatal("NewAssetDestResolver must return a non-nil resolver for a non-nil admin")
	}

	got, err := r.Resolve(context.Background(), &asset.ResolveRequest{
		FolderID:        "root-folder-id",
		FolderPath:      "Di-Awl0XyQs_Celebrity_Interviews",
		SubfolderName:   "Di-Awl0XyQs_Celebrity_Interviews",
		CreateSubfolder: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.FolderID != "child-folder-id" {
		t.Fatalf("FolderID = %q, want child-folder-id", got.FolderID)
	}
	if got.FolderPath != "Di-Awl0XyQs_Celebrity_Interviews" {
		t.Fatalf("FolderPath = %q, want the transport-computed label preserved", got.FolderPath)
	}
	if admin.getOrCreateCalls != 1 || admin.lastName != "Di-Awl0XyQs_Celebrity_Interviews" || admin.lastParentID != "root-folder-id" {
		t.Fatalf("EnsureFolderPath must call GetOrCreateFolder with (subfolder, root): calls=%d name=%q parent=%q",
			admin.getOrCreateCalls, admin.lastName, admin.lastParentID)
	}
}

func TestAssetDestResolver_PassThroughExplicitFolder_NoDriveIO(t *testing.T) {
	admin := &fakeAdmin{}
	r := NewAssetDestResolver(admin)

	got, err := r.Resolve(context.Background(), &asset.ResolveRequest{
		FolderID:   "explicit-folder-id",
		FolderPath: "explicit/path",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.FolderID != "explicit-folder-id" || got.FolderPath != "explicit/path" {
		t.Fatalf("explicit folder must pass through verbatim: %+v", got)
	}
	if admin.getOrCreateCalls != 0 {
		t.Fatalf("explicit folder (no CreateSubfolder) must not call GetOrCreateFolder; called %d times", admin.getOrCreateCalls)
	}
}

func TestAssetDestResolver_CreateSubfolderWithoutParent_FailsClosed(t *testing.T) {
	r := NewAssetDestResolver(&fakeAdmin{cannedID: "child"})

	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{
		SubfolderName:   "sub",
		CreateSubfolder: true,
	})
	if err == nil {
		t.Fatal("CreateSubfolder without a parent FolderID must fail closed")
	}
	if !errors.Is(err, ErrAssetDestParentRequired) {
		t.Fatalf("error must wrap ErrAssetDestParentRequired; got %v", err)
	}
}

func TestAssetDestResolver_GetOrCreateError_FailsClosedTyped(t *testing.T) {
	admin := &fakeAdmin{getOrCreateErr: context.DeadlineExceeded}
	r := NewAssetDestResolver(admin)

	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{
		FolderID:        "root",
		SubfolderName:   "sub",
		CreateSubfolder: true,
	})
	if err == nil {
		t.Fatal("get-or-create failure must surface as an error")
	}
	if !errors.Is(err, ErrAssetDestSubfolderFailed) {
		t.Fatalf("error must wrap ErrAssetDestSubfolderFailed (typed fail-closed); got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error must preserve the underlying cause via the %v chain; got %v", "%w", err)
	}
}

func TestAssetDestResolver_EmptyChildID_FailsClosedTyped(t *testing.T) {
	admin := &fakeAdmin{cannedID: ""}
	r := NewAssetDestResolver(admin)

	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{
		FolderID:        "root",
		SubfolderName:   "sub",
		CreateSubfolder: true,
	})
	if err == nil {
		t.Fatal("empty child folder id must surface as an error")
	}
	if !errors.Is(err, ErrAssetDestEmptyFolderID) {
		t.Fatalf("error must wrap ErrAssetDestEmptyFolderID; got %v", err)
	}
}

func TestNewAssetDestResolver_NilAdmin_ReturnsNil(t *testing.T) {
	if got := NewAssetDestResolver(nil); got != nil {
		t.Fatalf("NewAssetDestResolver(nil) = %v, want nil (typed-nil safety)", got)
	}
}

// TestAssetDestResolver_NilReceiver_FailsClosedTyped pins the godlike/07
// nil-receiver contract: a typed-nil resolver must surface the canonical
// sentinel instead of panicking.
func TestAssetDestResolver_NilReceiver_FailsClosedTyped(t *testing.T) {
	var r *AssetDestResolver
	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{
		FolderID:        "root",
		SubfolderName:   "sub",
		CreateSubfolder: true,
	})
	if err == nil {
		t.Fatal("nil receiver must fail closed")
	}
	if !errors.Is(err, ErrAssetDestResolverNotWired) {
		t.Fatalf("error must wrap ErrAssetDestResolverNotWired; got %v", err)
	}
	if !strings.Contains(err.Error(), "composition gap") {
		t.Fatalf("unexpected error: %v", err)
	}
}

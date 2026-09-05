package assets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	infradrive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type fakeDestAdmin struct {
	getOrCreateCalls int
	lastName         string
	lastParentID     string
	cannedID         string
	getOrCreateErr   error
}

func (f *fakeDestAdmin) GetOrCreateFolder(_ context.Context, name, parentID string) (string, error) {
	f.getOrCreateCalls++
	f.lastName = name
	f.lastParentID = parentID
	return f.cannedID, f.getOrCreateErr
}

func (f *fakeDestAdmin) GetFolderName(context.Context, string) (string, error)  { return "", nil }
func (f *fakeDestAdmin) TrashFolder(context.Context, string) error              { return nil }
func (f *fakeDestAdmin) DeleteFolder(context.Context, string) error             { return nil }
func (f *fakeDestAdmin) TrashFile(context.Context, string) error                { return nil }
func (f *fakeDestAdmin) DeleteFile(context.Context, string) error               { return nil }
func (f *fakeDestAdmin) MoveFile(context.Context, string, string, string) error { return nil }
func (f *fakeDestAdmin) RenameFile(context.Context, string, string) error       { return nil }
func (f *fakeDestAdmin) Ping(context.Context) error                             { return nil }

var _ infradrive.Admin = (*fakeDestAdmin)(nil)

func TestDestResolverCreateSubfolderUsesGetOrCreate(t *testing.T) {
	admin := &fakeDestAdmin{cannedID: "child-folder-id"}
	r := NewDestResolver(admin)
	if r == nil {
		t.Fatal("NewDestResolver must return a non-nil resolver for a non-nil admin")
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
		t.Fatalf("EnsureFolderPath must call GetOrCreateFolder with (subfolder, root): calls=%d name=%q parent=%q", admin.getOrCreateCalls, admin.lastName, admin.lastParentID)
	}
}

func TestDestResolverPassThroughExplicitFolderNoDriveIO(t *testing.T) {
	admin := &fakeDestAdmin{}
	r := NewDestResolver(admin)

	got, err := r.Resolve(context.Background(), &asset.ResolveRequest{FolderID: "explicit-folder-id", FolderPath: "explicit/path"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.FolderID != "explicit-folder-id" || got.FolderPath != "explicit/path" {
		t.Fatalf("explicit folder must pass through verbatim: %+v", got)
	}
	if admin.getOrCreateCalls != 0 {
		t.Fatalf("explicit folder must not call GetOrCreateFolder; called %d times", admin.getOrCreateCalls)
	}
}

func TestDestResolverCreateSubfolderWithoutParentFailsClosed(t *testing.T) {
	r := NewDestResolver(&fakeDestAdmin{cannedID: "child"})
	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{SubfolderName: "sub", CreateSubfolder: true})
	if err == nil {
		t.Fatal("CreateSubfolder without a parent FolderID must fail closed")
	}
	if !errors.Is(err, ErrDestParentRequired) {
		t.Fatalf("error must wrap ErrDestParentRequired; got %v", err)
	}
}

func TestDestResolverGetOrCreateErrorFailsClosedTyped(t *testing.T) {
	admin := &fakeDestAdmin{getOrCreateErr: context.DeadlineExceeded}
	r := NewDestResolver(admin)
	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{FolderID: "root", SubfolderName: "sub", CreateSubfolder: true})
	if err == nil {
		t.Fatal("get-or-create failure must surface as an error")
	}
	if !errors.Is(err, ErrDestSubfolderFailed) {
		t.Fatalf("error must wrap ErrDestSubfolderFailed; got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error must preserve the underlying cause; got %v", err)
	}
}

func TestDestResolverEmptyChildIDFailsClosedTyped(t *testing.T) {
	r := NewDestResolver(&fakeDestAdmin{cannedID: ""})
	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{FolderID: "root", SubfolderName: "sub", CreateSubfolder: true})
	if err == nil {
		t.Fatal("empty child folder id must surface as an error")
	}
	if !errors.Is(err, ErrDestEmptyFolderID) {
		t.Fatalf("error must wrap ErrDestEmptyFolderID; got %v", err)
	}
}

func TestNewDestResolverNilAdminReturnsNil(t *testing.T) {
	if got := NewDestResolver(nil); got != nil {
		t.Fatalf("NewDestResolver(nil) = %v, want nil", got)
	}
}

func TestDestResolverNilReceiverFailsClosedTyped(t *testing.T) {
	var r *DestResolver
	_, err := r.Resolve(context.Background(), &asset.ResolveRequest{FolderID: "root", SubfolderName: "sub", CreateSubfolder: true})
	if err == nil {
		t.Fatal("nil receiver must fail closed")
	}
	if !errors.Is(err, ErrDestResolverNotWired) {
		t.Fatalf("error must wrap ErrDestResolverNotWired; got %v", err)
	}
	if !strings.Contains(err.Error(), "composition gap") {
		t.Fatalf("unexpected error: %v", err)
	}
}

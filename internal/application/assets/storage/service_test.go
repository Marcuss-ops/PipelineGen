// Package storage — service_test.go tests the application-layer
// storage Service with a fake DrivePort. No real Drive, no Gin, no HTTP.
package storage

import (
	"context"
	"errors"
	"testing"
)

// ── Fake implementations ────────────────────────────────────────────

// fakeDrivePort implements DrivePort with configurable results and
// recorded arguments for assertion.
type fakeDrivePort struct {
	// Results
	listFilesResult []DriveFile
	listFilesErr    error
	moveFileErr     error
	createFolderID  string
	createFolderErr error
	renameFileErr   error

	// Captured args
	lastListFolderID  string
	lastMoveFileID    string
	lastMoveFromFolder string
	lastMoveToFolder  string
	lastCreateName    string
	lastCreateParent  string
	lastRenameFileID  string
	lastRenameNewName string

	// Call counters
	listFilesCalled   int
	moveFileCalled    int
	getOrCreateCalled int
	renameFileCalled  int

	// If checkCtx is set, methods verify ctx is not nil before proceeding.
	checkCtx bool
	ctxErr   error // returned when checkCtx is true and ctx.Err() is non-nil
}

func (f *fakeDrivePort) ListFiles(ctx context.Context, folderID string) ([]DriveFile, error) {
	f.listFilesCalled++
	f.lastListFolderID = folderID
	if f.checkCtx && ctx.Err() != nil {
		return nil, f.ctxErr
	}
	if f.listFilesErr != nil {
		return nil, f.listFilesErr
	}
	return f.listFilesResult, nil
}

func (f *fakeDrivePort) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	f.moveFileCalled++
	f.lastMoveFileID = fileID
	f.lastMoveFromFolder = fromFolderID
	f.lastMoveToFolder = toFolderID
	if f.checkCtx && ctx.Err() != nil {
		return f.ctxErr
	}
	return f.moveFileErr
}

func (f *fakeDrivePort) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	f.getOrCreateCalled++
	f.lastCreateName = name
	f.lastCreateParent = parentID
	if f.checkCtx && ctx.Err() != nil {
		return "", f.ctxErr
	}
	if f.createFolderErr != nil {
		return "", f.createFolderErr
	}
	return f.createFolderID, nil
}

func (f *fakeDrivePort) RenameFile(ctx context.Context, fileID, newName string) error {
	f.renameFileCalled++
	f.lastRenameFileID = fileID
	f.lastRenameNewName = newName
	if f.checkCtx && ctx.Err() != nil {
		return f.ctxErr
	}
	return f.renameFileErr
}

// fakeLogger implements Logger, counting calls.
type fakeLogger struct {
	infoCalled  int
	warnCalled  int
	errorCalled int
	debugCalled int
}

func (l *fakeLogger) Info(msg string, keysAndValues ...any)  { l.infoCalled++ }
func (l *fakeLogger) Warn(msg string, keysAndValues ...any)  { l.warnCalled++ }
func (l *fakeLogger) Error(msg string, keysAndValues ...any) { l.errorCalled++ }
func (l *fakeLogger) Debug(msg string, keysAndValues ...any) { l.debugCalled++ }

// Compile-time assertion: fakeDrivePort implements DrivePort.
var _ DrivePort = (*fakeDrivePort)(nil)

// ── Tests ──────────────────────────────────────────────────────────

func TestNewService_ReturnsNonNull(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
}

func TestListFiles_Success(t *testing.T) {
	expected := []DriveFile{{ID: "1", Name: "foo.mp4", MimeType: "video/mp4"}}
	fakeDrive := &fakeDrivePort{listFilesResult: expected}
	svc := NewService(fakeDrive, &fakeLogger{})

	result, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "root"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("Count=%d want 1", result.Count)
	}
	if result.Files[0].Name != "foo.mp4" {
		t.Errorf("Files[0].Name=%q want foo.mp4", result.Files[0].Name)
	}
	if fakeDrive.listFilesCalled != 1 {
		t.Errorf("listFilesCalled=%d want 1", fakeDrive.listFilesCalled)
	}
	if fakeDrive.lastListFolderID != "root" {
		t.Errorf("lastListFolderID=%q want root", fakeDrive.lastListFolderID)
	}
}

func TestListFiles_EmptyFolderID(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	_, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: ""})
	if err == nil {
		t.Fatal("expected error on empty folder_id")
	}
}

func TestListFiles_DriveError(t *testing.T) {
	sentinel := errors.New("drive unavailable")
	fakeDrive := &fakeDrivePort{listFilesErr: sentinel}
	log := &fakeLogger{}
	svc := NewService(fakeDrive, log)

	_, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "root"})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel); got %v", err)
	}
	if log.errorCalled == 0 {
		t.Errorf("expected error to be logged")
	}
}

func TestListFiles_NilDrivePort(t *testing.T) {
	svc := NewService(nil, &fakeLogger{})
	_, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "root"})
	if err == nil {
		t.Fatal("expected error on nil drive port")
	}
}

func TestMoveFiles_Success(t *testing.T) {
	fakeDrive := &fakeDrivePort{}
	svc := NewService(fakeDrive, &fakeLogger{})

	result, err := svc.MoveFiles(context.Background(), MoveFilesRequest{
		FileIDs:    []string{"f1", "f2"},
		ToFolderID: "dest",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Moved != 2 {
		t.Errorf("Moved=%d want 2", result.Moved)
	}
	if fakeDrive.moveFileCalled != 2 {
		t.Errorf("moveFileCalled=%d want 2", fakeDrive.moveFileCalled)
	}
}

func TestMoveFiles_PartialFailure(t *testing.T) {
	fakeDrive := &fakeDrivePort{moveFileErr: errors.New("boom")}
	svc := NewService(fakeDrive, &fakeLogger{})

	result, err := svc.MoveFiles(context.Background(), MoveFilesRequest{
		FileIDs:    []string{"f1", "f2"},
		ToFolderID: "dest",
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Moved != 0 {
		t.Errorf("Moved=%d want 0 (all failed)", result.Moved)
	}
	if result.ErrorCount != 2 {
		t.Errorf("ErrorCount=%d want 2", result.ErrorCount)
	}
}

func TestMoveFiles_EmptyToFolderID(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	_, err := svc.MoveFiles(context.Background(), MoveFilesRequest{FileIDs: []string{"f1"}, ToFolderID: ""})
	if err == nil {
		t.Fatal("expected error on empty to_folder_id")
	}
}

func TestMoveFiles_EmptyFileIDs(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	_, err := svc.MoveFiles(context.Background(), MoveFilesRequest{FileIDs: nil, ToFolderID: "dest"})
	if err == nil {
		t.Fatal("expected error on empty file_ids")
	}
}

func TestMoveFiles_NilDrivePort(t *testing.T) {
	svc := NewService(nil, &fakeLogger{})
	_, err := svc.MoveFiles(context.Background(), MoveFilesRequest{
		FileIDs:    []string{"f1"},
		ToFolderID: "dest",
	})
	if err == nil {
		t.Fatal("expected error on nil drive port")
	}
}

func TestCreateFolder_Success(t *testing.T) {
	fakeDrive := &fakeDrivePort{createFolderID: "new-folder-id"}
	svc := NewService(fakeDrive, &fakeLogger{})

	result, err := svc.CreateFolder(context.Background(), CreateFolderRequest{
		Name:     "my-folder",
		ParentID: "parent-id",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FolderID != "new-folder-id" {
		t.Errorf("FolderID=%q want new-folder-id", result.FolderID)
	}
	if fakeDrive.getOrCreateCalled != 1 {
		t.Errorf("getOrCreateCalled=%d want 1", fakeDrive.getOrCreateCalled)
	}
	if fakeDrive.lastCreateName != "my-folder" {
		t.Errorf("lastCreateName=%q want my-folder", fakeDrive.lastCreateName)
	}
	if fakeDrive.lastCreateParent != "parent-id" {
		t.Errorf("lastCreateParent=%q want parent-id", fakeDrive.lastCreateParent)
	}
}

func TestCreateFolder_EmptyName(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	_, err := svc.CreateFolder(context.Background(), CreateFolderRequest{Name: ""})
	if err == nil {
		t.Fatal("expected error on empty name")
	}
}

func TestCreateFolder_DriveError(t *testing.T) {
	fakeDrive := &fakeDrivePort{createFolderErr: errors.New("quota exceeded")}
	log := &fakeLogger{}
	svc := NewService(fakeDrive, log)

	_, err := svc.CreateFolder(context.Background(), CreateFolderRequest{Name: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if log.errorCalled == 0 {
		t.Errorf("expected error to be logged")
	}
}

func TestCreateFolder_NilDrivePort(t *testing.T) {
	svc := NewService(nil, &fakeLogger{})
	_, err := svc.CreateFolder(context.Background(), CreateFolderRequest{Name: "x"})
	if err == nil {
		t.Fatal("expected error on nil drive port")
	}
}

func TestRenameFile_Success(t *testing.T) {
	fakeDrive := &fakeDrivePort{}
	svc := NewService(fakeDrive, &fakeLogger{})

	result, err := svc.RenameFile(context.Background(), RenameFileRequest{
		FileID:  "f1",
		NewName: "renamed.mp4",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NewName != "renamed.mp4" {
		t.Errorf("NewName=%q", result.NewName)
	}
	if fakeDrive.lastRenameFileID != "f1" {
		t.Errorf("lastRenameFileID=%q", fakeDrive.lastRenameFileID)
	}
}

func TestRenameFile_EmptyFileID(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	_, err := svc.RenameFile(context.Background(), RenameFileRequest{FileID: "", NewName: "x"})
	if err == nil {
		t.Fatal("expected error on empty file_id")
	}
}

func TestRenameFile_EmptyNewName(t *testing.T) {
	svc := NewService(&fakeDrivePort{}, &fakeLogger{})
	_, err := svc.RenameFile(context.Background(), RenameFileRequest{FileID: "f1", NewName: ""})
	if err == nil {
		t.Fatal("expected error on empty new_name")
	}
}

func TestRenameFile_DriveError(t *testing.T) {
	sentinel := errors.New("permission denied")
	fakeDrive := &fakeDrivePort{renameFileErr: sentinel}
	log := &fakeLogger{}
	svc := NewService(fakeDrive, log)

	_, err := svc.RenameFile(context.Background(), RenameFileRequest{FileID: "f1", NewName: "x"})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel); got %v", err)
	}
	if log.errorCalled == 0 {
		t.Errorf("expected error to be logged")
	}
}

func TestRenameFile_NilDrivePort(t *testing.T) {
	svc := NewService(nil, &fakeLogger{})
	_, err := svc.RenameFile(context.Background(), RenameFileRequest{FileID: "f1", NewName: "x"})
	if err == nil {
		t.Fatal("expected error on nil drive port")
	}
}

func TestService_ContextCancellation(t *testing.T) {
	// Fake checks ctx.Err() and returns context.Canceled.
	fakeDrive := &fakeDrivePort{checkCtx: true, ctxErr: context.Canceled}
	svc := NewService(fakeDrive, &fakeLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.ListFiles(ctx, ListFilesRequest{FolderID: "root"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestService_NilLogger_ErrorPathDoesNotPanic(t *testing.T) {
	// Exercise the error path with nil logger: drive returns an error,
	// and the service must NOT panic when logging it.
	sentinel := errors.New("upload failed")
	fakeDrive := &fakeDrivePort{listFilesErr: sentinel}
	svc := NewService(fakeDrive, nil) // nil logger

	_, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "root"})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
	// The test passing without panic proves nil logger is safe on error paths.
}

func TestService_NilLogger_SuccessPath(t *testing.T) {
	fakeDrive := &fakeDrivePort{listFilesResult: []DriveFile{{ID: "1", Name: "x"}}}
	svc := NewService(fakeDrive, nil) // nil logger
	result, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "root"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("Count=%d want 1", result.Count)
	}
}

func TestService_ActuallyDelegatesToPort(t *testing.T) {
	// Verify the service actually calls the port, not hard-coded success.
	fakeDrive := &fakeDrivePort{}
	svc := NewService(fakeDrive, &fakeLogger{})

	_, _ = svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "r"})
	if fakeDrive.listFilesCalled != 1 {
		t.Error("ListFiles did NOT delegate to DrivePort")
	}

	_, _ = svc.MoveFiles(context.Background(), MoveFilesRequest{FileIDs: []string{"f1"}, ToFolderID: "d"})
	if fakeDrive.moveFileCalled != 1 {
		t.Error("MoveFiles did NOT delegate to DrivePort")
	}

	_, _ = svc.CreateFolder(context.Background(), CreateFolderRequest{Name: "x"})
	if fakeDrive.getOrCreateCalled != 1 {
		t.Error("CreateFolder did NOT delegate to DrivePort")
	}

	_, _ = svc.RenameFile(context.Background(), RenameFileRequest{FileID: "f1", NewName: "x"})
	if fakeDrive.renameFileCalled != 1 {
		t.Error("RenameFile did NOT delegate to DrivePort")
	}
}

func TestService_PreservesErrorChain(t *testing.T) {
	// Verify errors.Is and error wrapping through service layer.
	baseErr := errors.New("root cause")
	fakeDrive := &fakeDrivePort{listFilesErr: baseErr}
	svc := NewService(fakeDrive, &fakeLogger{})

	_, err := svc.ListFiles(context.Background(), ListFilesRequest{FolderID: "root"})
	if !errors.Is(err, baseErr) {
		t.Errorf("errors.Is(err, baseErr) should be true; got %v", err)
	}
	if err.Error() == baseErr.Error() {
		// The service wraps the error with context ("list files: %w").
		// If they're identical, wrapping is missing.
		t.Log("note: error message equals base error (wrapping may have been elided)")
	}
}

package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubFolderResolver records the last resolve call and returns configurable values.
type stubFolderResolver struct {
	lastGroup      string
	lastSubject    string
	lastRootFolder string
	folderID       string
	err            error
}

func (s *stubFolderResolver) ResolveFolder(_ context.Context, group string, subject string, rootFolderOverride string) (string, error) {
	s.lastGroup = group
	s.lastSubject = subject
	s.lastRootFolder = rootFolderOverride
	return s.folderID, s.err
}

// stubFileUploader records the last upload call and returns configurable values.
type stubFileUploader struct {
	lastFolderID    string
	lastLocalPath   string
	lastFilename    string
	lastDescription string
	lastAssetID     string
	result          *UploadFileResult
	err             error
}

func (s *stubFileUploader) UploadFile(_ context.Context, folderID string, localPath string, filename string, description string, assetID string) (*UploadFileResult, error) {
	s.lastFolderID = folderID
	s.lastLocalPath = localPath
	s.lastFilename = filename
	s.lastDescription = description
	s.lastAssetID = assetID
	return s.result, s.err
}

// ── Test 1: happy path — both ports succeed, full result populated ───────

func TestPublishToDrive_HappyPath(t *testing.T) {
	resolver := &stubFolderResolver{folderID: "drive-folder-456"}
	uploader := &stubFileUploader{
		result: &UploadFileResult{
			FileID:      "drive-file-123",
			WebViewLink: "https://drive.google.com/file/d/drive-file-123/view",
		},
	}

	cmd := PublishToDriveCommand{
		AssetID:     "yt_dQw4w9WgXcQ_a1b2c3d4",
		Group:       "test-group",
		Subject:     "dQw4w9WgXcQ-my-video",
		RootFolder:  "root-789",
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "dQw4w9WgXcQ - My Video.mp4",
		Description: "Name: My Video",
	}

	result, err := PublishToDrive(context.Background(), resolver, uploader, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.FolderResolved {
		t.Error("expected FolderResolved=true")
	}
	if !result.FileUploaded {
		t.Error("expected FileUploaded=true")
	}
	if result.FolderID != "drive-folder-456" {
		t.Errorf("expected FolderID drive-folder-456, got %q", result.FolderID)
	}
	if result.FileID != "drive-file-123" {
		t.Errorf("expected FileID drive-file-123, got %q", result.FileID)
	}
	if result.WebViewLink != "https://drive.google.com/file/d/drive-file-123/view" {
		t.Errorf("expected WebViewLink, got %q", result.WebViewLink)
	}

	// Verify folder resolver received the correct fields.
	if resolver.lastGroup != cmd.Group {
		t.Errorf("expected resolver Group %q, got %q", cmd.Group, resolver.lastGroup)
	}
	if resolver.lastSubject != cmd.Subject {
		t.Errorf("expected resolver Subject %q, got %q", cmd.Subject, resolver.lastSubject)
	}
	if resolver.lastRootFolder != cmd.RootFolder {
		t.Errorf("expected resolver RootFolder %q, got %q", cmd.RootFolder, resolver.lastRootFolder)
	}

	// Verify uploader received the folder from the resolver, not the command.
	if uploader.lastFolderID != "drive-folder-456" {
		t.Errorf("expected uploader FolderID drive-folder-456, got %q", uploader.lastFolderID)
	}
	if uploader.lastLocalPath != cmd.LocalPath {
		t.Errorf("expected uploader LocalPath %q, got %q", cmd.LocalPath, uploader.lastLocalPath)
	}
}

// ── Test 2: nil folder resolver → skip resolution, uploader still fires ──

func TestPublishToDrive_NilResolver_SkipsFolder(t *testing.T) {
	uploader := &stubFileUploader{
		result: &UploadFileResult{FileID: "file-no-folder"},
	}

	cmd := PublishToDriveCommand{
		AssetID:   "yt_test",
		LocalPath: "/tmp/clip.mp4",
		Filename:  "test.mp4",
	}

	result, err := PublishToDrive(context.Background(), nil, uploader, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.FolderResolved {
		t.Error("expected FolderResolved=false when resolver is nil")
	}
	if !result.FileUploaded {
		t.Error("expected FileUploaded=true — uploader should still fire")
	}
	// Uploader received empty folder ID (no resolver ran).
	if uploader.lastFolderID != "" {
		t.Errorf("expected uploader FolderID to be empty, got %q", uploader.lastFolderID)
	}
	if result.FileID != "file-no-folder" {
		t.Errorf("expected FileID file-no-folder, got %q", result.FileID)
	}
}

// ── Test 3: nil file uploader → folder resolved, upload skipped ──────────

func TestPublishToDrive_NilUploader_SkipsUpload(t *testing.T) {
	resolver := &stubFolderResolver{folderID: "drive-folder-resolved"}

	cmd := PublishToDriveCommand{
		AssetID: "yt_test",
		Group:   "group-x",
		Subject: "subject-y",
	}

	result, err := PublishToDrive(context.Background(), resolver, nil, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.FolderResolved {
		t.Error("expected FolderResolved=true — resolver should still fire")
	}
	if result.FileUploaded {
		t.Error("expected FileUploaded=false when uploader is nil")
	}
	if result.FolderID != "drive-folder-resolved" {
		t.Errorf("expected FolderID drive-folder-resolved, got %q", result.FolderID)
	}
}

// ── Test 4: both ports nil → no-op, both flags false, no error ───────────

func TestPublishToDrive_BothNil_NoOp(t *testing.T) {
	cmd := PublishToDriveCommand{
		AssetID:   "yt_fallback",
		LocalPath: "/tmp/clip.mp4",
	}

	result, err := PublishToDrive(context.Background(), nil, nil, cmd)
	if err != nil {
		t.Fatalf("expected nil error for dual-nil ports, got %v", err)
	}
	if result.FolderResolved {
		t.Error("expected FolderResolved=false when resolver is nil")
	}
	if result.FileUploaded {
		t.Error("expected FileUploaded=false when uploader is nil")
	}
}

// ── Test 5: uploader error — folder resolved, upload fails, partial result ──

func TestPublishToDrive_UploaderError_PartialSuccess(t *testing.T) {
	sentinel := errors.New("drive: upload bandwidth exceeded")
	resolver := &stubFolderResolver{folderID: "drive-folder-ok"}
	uploader := &stubFileUploader{err: sentinel}

	cmd := PublishToDriveCommand{
		AssetID:   "yt_test",
		LocalPath: "/tmp/clip.mp4",
		Filename:  "test.mp4",
	}

	result, err := PublishToDrive(context.Background(), resolver, uploader, cmd)
	if err == nil {
		t.Fatal("expected non-nil error when uploader fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.PublishToDrive") {
		t.Errorf("expected error to wrap with usecase prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "upload file") {
		t.Errorf("expected 'upload file' in error chain, got %v", err)
	}
	// Folder resolved successfully — partial state reflected in result.
	if !result.FolderResolved {
		t.Error("expected FolderResolved=true — folder resolved before upload failed")
	}
	if result.FileUploaded {
		t.Error("expected FileUploaded=false when uploader fails")
	}
	if result.FolderID != "drive-folder-ok" {
		t.Errorf("expected FolderID drive-folder-ok, got %q", result.FolderID)
	}
}

// ── Test 6: folder resolver error → fail-closed, uploader never called ───

func TestPublishToDrive_ResolverError_Aborts(t *testing.T) {
	sentinel := errors.New("drive: folder quota exceeded")
	resolver := &stubFolderResolver{err: sentinel}
	uploader := &stubFileUploader{result: &UploadFileResult{FileID: "should-not-reach"}}

	cmd := PublishToDriveCommand{
		AssetID:   "yt_test",
		LocalPath: "/tmp/clip.mp4",
	}

	result, err := PublishToDrive(context.Background(), resolver, uploader, cmd)
	if err == nil {
		t.Fatal("expected non-nil error when resolver fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.PublishToDrive") {
		t.Errorf("expected error to wrap with usecase prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "resolve folder") {
		t.Errorf("expected 'resolve folder' in error chain, got %v", err)
	}
	// Uploader must NOT have been called.
	if uploader.lastLocalPath != "" {
		t.Errorf("expected uploader to NOT be called after resolver error, got localPath=%q", uploader.lastLocalPath)
	}
	// Result is still returned (partial state).
	if result.FolderResolved {
		t.Error("expected FolderResolved=false on resolver error")
	}
}

package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubPublisher is a test-only DrivePublisher that records the last request
// and returns a configurable result/error.
type stubPublisher struct {
	lastReq PublishRequest
	result  *PublishResult
	err     error
}

func (s *stubPublisher) Publish(_ context.Context, req PublishRequest) (*PublishResult, error) {
	s.lastReq = req
	return s.result, s.err
}

// ── Test 1: happy path — publisher succeeds, all result fields populated ──

func TestPublishClipToDrive_HappyPath(t *testing.T) {
	stub := &stubPublisher{
		result: &PublishResult{
			FileID:      "drive-file-123",
			WebViewLink: "https://drive.google.com/file/d/drive-file-123/view",
			FolderID:    "drive-folder-456",
		},
	}

	cmd := PublishClipCommand{
		AssetID:     "yt_dQw4w9WgXcQ_a1b2c3d4",
		Group:       "test-group",
		Subject:     "dQw4w9WgXcQ-my-video",
		RootFolder:  "root-789",
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "dQw4w9WgXcQ - My Video.mp4",
		Description: "Name: My Video\nCategory: sports",
	}

	result, err := PublishClipToDrive(context.Background(), stub, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.Published {
		t.Error("expected Published=true")
	}
	if result.FileID != "drive-file-123" {
		t.Errorf("expected FileID drive-file-123, got %q", result.FileID)
	}
	if result.FolderID != "drive-folder-456" {
		t.Errorf("expected FolderID drive-folder-456, got %q", result.FolderID)
	}

	// Verify all command fields flowed through to the request.
	req := stub.lastReq
	if req.Destination != "youtube-clip" {
		t.Errorf("expected Destination youtube-clip, got %q", req.Destination)
	}
	if req.AssetID != cmd.AssetID {
		t.Errorf("expected AssetID %q, got %q", cmd.AssetID, req.AssetID)
	}
	if req.Group != cmd.Group {
		t.Errorf("expected Group %q, got %q", cmd.Group, req.Group)
	}
	if req.Subject != cmd.Subject {
		t.Errorf("expected Subject %q, got %q", cmd.Subject, req.Subject)
	}
	if req.RootFolderOverride != cmd.RootFolder {
		t.Errorf("expected RootFolderOverride %q, got %q", cmd.RootFolder, req.RootFolderOverride)
	}
	if req.LocalPath != cmd.LocalPath {
		t.Errorf("expected LocalPath %q, got %q", cmd.LocalPath, req.LocalPath)
	}
	if req.Filename != cmd.Filename {
		t.Errorf("expected Filename %q, got %q", cmd.Filename, req.Filename)
	}
	if req.Description != cmd.Description {
		t.Errorf("expected Description %q, got %q", cmd.Description, req.Description)
	}
}

// ── Test 2: nil publisher → Published=false, no error ──────────────────────

func TestPublishClipToDrive_NilPublisher_ReturnsNotPublished(t *testing.T) {
	cmd := PublishClipCommand{
		AssetID:   "yt_test_id",
		LocalPath: "/tmp/clip.mp4",
		Filename:  "test.mp4",
	}

	result, err := PublishClipToDrive(context.Background(), nil, cmd)
	if err != nil {
		t.Fatalf("expected nil error for nil publisher, got %v", err)
	}
	if result.Published {
		t.Error("expected Published=false when publisher is nil")
	}
}

// ── Test 3: publisher error → Published=false, wrapped error ───────────────

func TestPublishClipToDrive_PublisherError_ReturnsNotPublished(t *testing.T) {
	sentinel := errors.New("drive quota exceeded")
	stub := &stubPublisher{err: sentinel}

	cmd := PublishClipCommand{
		AssetID:   "yt_test_id",
		LocalPath: "/tmp/clip.mp4",
		Filename:  "test.mp4",
	}

	result, err := PublishClipToDrive(context.Background(), stub, cmd)
	if err == nil {
		t.Fatal("expected non-nil error when publisher fails")
	}
	if result.Published {
		t.Error("expected Published=false when publisher errors")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.PublishClipToDrive") {
		t.Errorf("expected error to wrap with usecase prefix, got %v", err)
	}
}

// ── Test 4: empty optional fields (Group, Subject, RootFolder) ────────────

func TestPublishClipToDrive_EmptyOptionals_StillPublishes(t *testing.T) {
	stub := &stubPublisher{
		result: &PublishResult{
			FileID:      "file-minimal",
			WebViewLink: "https://drive.google.com/file/d/file-minimal/view",
			FolderID:    "folder-minimal",
		},
	}

	cmd := PublishClipCommand{
		AssetID:   "yt_minimal",
		LocalPath: "/tmp/minimal.mp4",
		Filename:  "minimal.mp4",
		// Group, Subject, RootFolder are intentionally empty.
	}

	result, err := PublishClipToDrive(context.Background(), stub, cmd)
	if err != nil {
		t.Fatalf("expected nil error for minimal command, got %v", err)
	}
	if !result.Published {
		t.Error("expected Published=true for minimal command")
	}
	if result.FileID != "file-minimal" {
		t.Errorf("expected FileID file-minimal, got %q", result.FileID)
	}
	// Empty optional fields should pass through as empty strings.
	if stub.lastReq.Group != "" {
		t.Errorf("expected Group to be empty, got %q", stub.lastReq.Group)
	}
	if stub.lastReq.Subject != "" {
		t.Errorf("expected Subject to be empty, got %q", stub.lastReq.Subject)
	}
}

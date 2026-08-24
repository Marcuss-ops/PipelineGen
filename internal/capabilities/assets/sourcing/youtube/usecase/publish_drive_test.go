package assets

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

// ── PR-YT-CLIP-SEMANTIC-LOCATION-FIX tests ────────────────────────────────

// TestPublishClipToDrive_SemanticCategory_FlowsToRequest verifies that
// Category, Provider, Tags, and Language flow through from PublishClipCommand
// to the PublishRequest that reaches the Publisher. This is the canonical
// contract: a payload with location={category:"Boxe", subject:"Pacquiao vs
// Broner", provider:"youtube"} must result in the Publisher receiving those
// fields so YouTubeClipPath can build clips/Boxe/Pacquiao vs Broner/.
func TestPublishClipToDrive_SemanticCategory_FlowsToRequest(t *testing.T) {
	stub := &stubPublisher{
		result: &PublishResult{
			FileID:      "drive-semantic-1",
			WebViewLink: "https://drive.google.com/file/d/drive-semantic-1/view",
			FolderID:    "folder-semantic-1",
		},
	}

	cmd := PublishClipCommand{
		AssetID:     "yt_pacquiao_a1b2c3d4",
		Group:       "Boxe",
		Subject:     "dQw4w9WgXcQ-pacquiao-vs-broner",
		LocalPath:   "/tmp/pacquiao.mp4",
		Filename:    "dQw4w9WgXcQ - Pacquiao vs Broner.mp4",
		Description: "Name: Pacquiao vs Broner\nCategory: Boxe",
		Category:    "Boxe",
		Provider:    "youtube",
		Tags:        []string{"boxing", "pacquiao", "broner"},
		Language:    "en-US",
	}

	result, err := PublishClipToDrive(context.Background(), stub, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.Published {
		t.Error("expected Published=true")
	}

	req := stub.lastReq

	// Category must flow through so YouTubeClipPath can use it.
	if req.Category != "Boxe" {
		t.Errorf("expected Category 'Boxe', got %q", req.Category)
	}

	// Provider must flow through for Qdrant payload enrichment.
	if req.Provider != "youtube" {
		t.Errorf("expected Provider 'youtube', got %q", req.Provider)
	}

	// Tags must flow through for hybrid BM25 lexical search.
	if len(req.Tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(req.Tags), req.Tags)
	}
	if len(req.Tags) > 0 && req.Tags[0] != "boxing" {
		t.Errorf("expected Tags[0]='boxing', got %q", req.Tags[0])
	}

	// Language must flow through for BCP-47 metadata.
	if req.Language != "en-US" {
		t.Errorf("expected Language 'en-US', got %q", req.Language)
	}

	// Group and Subject must still be present for YouTubeClipPath.
	if req.Group != "Boxe" {
		t.Errorf("expected Group 'Boxe', got %q", req.Group)
	}
	if req.Subject != "dQw4w9WgXcQ-pacquiao-vs-broner" {
		t.Errorf("expected Subject non-empty, got %q", req.Subject)
	}

	// ParentFolderID is RETIRED (PR-P12-YOUTUBE-LEGACY-RETIRE, July 2026).
	// The canonical Publisher resolves the target folder via
	// DestinationRegistry + semantic fields (Category/Provider/Group/etc).
}

// TestPublishClipToDrive_SemanticFieldsEmptyByDefault verifies that
// existing callers who don't populate Category/Provider/Tags/Language
// get zero-value defaults (empty strings / nil slice). This is the
// backward-compat contract: pre-PR-YT-CLIP-SEMANTIC-LOCATION-FIX call
// sites compile and behave identically.
func TestPublishClipToDrive_SemanticFieldsEmptyByDefault(t *testing.T) {
	stub := &stubPublisher{
		result: &PublishResult{
			FileID:      "drive-legacy-1",
			WebViewLink: "https://drive.google.com/file/d/drive-legacy-1/view",
			FolderID:    "folder-legacy-1",
		},
	}

	// Legacy-style command: only Group/Subject/RootFolder, no semantic fields.
	cmd := PublishClipCommand{
		AssetID:     "yt_legacy_abc123",
		Group:       "legacy-group",
		Subject:     "legacy-subject",
		LocalPath:   "/tmp/legacy.mp4",
		Filename:    "legacy.mp4",
		Description: "legacy clip",
		// Category, Provider, Tags, Language intentionally zero-valued.
	}

	result, err := PublishClipToDrive(context.Background(), stub, cmd)
	if err != nil {
		t.Fatalf("expected nil error for legacy command, got %v", err)
	}
	if !result.Published {
		t.Error("expected Published=true for legacy command")
	}

	req := stub.lastReq

	// Semantic fields must be empty for backward-compat.
	if req.Category != "" {
		t.Errorf("expected Category empty for legacy command, got %q", req.Category)
	}
	if req.Provider != "" {
		t.Errorf("expected Provider empty for legacy command, got %q", req.Provider)
	}
	if len(req.Tags) != 0 {
		t.Errorf("expected Tags empty for legacy command, got %v", req.Tags)
	}
	if req.Language != "" {
		t.Errorf("expected Language empty for legacy command, got %q", req.Language)
	}

	// Legacy fields must still flow through.
	if req.Group != "legacy-group" {
		t.Errorf("expected Group 'legacy-group', got %q", req.Group)
	}
}

// TestPublishClipToDrive_ProjectID_FlowsToRequest verifies that
// ProjectID flows through from PublishClipCommand to PublishRequest.
// This is the canonical replacement for the retired ParentFolderID
// field — callers that need per-call folder targeting pass ProjectID
// instead (PR-P12-YOUTUBE-LEGACY-RETIRE, July 2026).
//
// RootFolder is intentionally NOT threaded to PublishRequest — the
// canonical Publisher resolves the target folder via DestinationRegistry
// + DestinationPolicy.RootFolderID.
func TestPublishClipToDrive_ProjectID_FlowsToRequest(t *testing.T) {
	stub := &stubPublisher{
		result: &PublishResult{
			FileID:      "drive-root-1",
			WebViewLink: "https://drive.google.com/file/d/drive-root-1/view",
			FolderID:    "folder-root-1",
		},
	}

	cmd := PublishClipCommand{
		AssetID:    "yt_root_test",
		Group:      "some-group",
		Subject:    "some-subject",
		ProjectID:  "explicit-project-id",
		RootFolder: "explicit-folder-id",
		LocalPath:  "/tmp/root.mp4",
		Filename:   "root.mp4",
	}

	result, err := PublishClipToDrive(context.Background(), stub, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !result.Published {
		t.Error("expected Published=true")
	}

	// ProjectID must pass through — this is the canonical replacement
	// for the retired ParentFolderID field.
	if stub.lastReq.ProjectID != "explicit-project-id" {
		t.Errorf("expected ProjectID 'explicit-project-id', got %q",
			stub.lastReq.ProjectID)
	}
}

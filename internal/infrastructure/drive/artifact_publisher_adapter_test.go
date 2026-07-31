package drive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ── Test stubs ──────────────────────────────────────────────────────

// stubDeliveryPublisher is a test double for delivery.Publisher.
type stubDeliveryPublisher struct {
	// publishFn is the canned Publish behaviour. When nil, the test
	// double returns a default successful PublishResult.
	publishFn func(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)

	// lastReq captures the most recent PublishRequest for assertions.
	lastReq *delivery.PublishRequest
}

func (s *stubDeliveryPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	s.lastReq = &req
	if s.publishFn != nil {
		return s.publishFn(ctx, req)
	}
	return &delivery.PublishResult{
		FileID:       "drive-file-123",
		WebViewLink:  "https://drive.google.com/file/d/drive-file-123/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-file-123",
		MD5Checksum:  "abc123",
		FolderID:     "folder-456",
		FolderPath:   "clips/test-channel",
		Destination:  req.Destination,
		Action:       delivery.PublishActionCreated,
	}, nil
}

func (s *stubDeliveryPublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	return "folder-456", nil
}

// ── Helper: write temp file with known content ──────────────────────

// writeTempFile writes content to a temp file under t.TempDir() and
// returns the path and its SHA-256 hex digest.
func writeTempFile(t *testing.T, content string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-artifact.bin")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	h := sha256.New()
	h.Write([]byte(content))
	sha := hex.EncodeToString(h.Sum(nil))
	return path, sha
}

// ── Test: Happy path — Publish OK → AssetLocation with PUBLISHED ────

func TestArtifactPublisherAdapter_Publish_HappyPath(t *testing.T) {
	content := "hello world artifact content"
	localPath, sha := writeTempFile(t, content)

	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	artifact := finalization.VerifiedArtifact{
		ArtifactID:     "test-artifact-001",
		Kind:           finalization.KindVideo,
		Filename:       "clip_abc123.mp4",
		LocalPath:      localPath,
		MIMEType:       "video/mp4",
		SizeBytes:      int64(len(content)),
		SHA256:         sha,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "idem-key-001",
	}

	loc, err := adapter.Publish(context.Background(), artifact)
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	// AssetLocation assertions.
	if loc.Provider != "drive" {
		t.Errorf("expected Provider='drive', got %q", loc.Provider)
	}
	if loc.FileID != "drive-file-123" {
		t.Errorf("expected FileID='drive-file-123', got %q", loc.FileID)
	}
	if loc.WebViewLink == "" {
		t.Error("expected non-empty WebViewLink")
	}
	if loc.DownloadLink == "" {
		t.Error("expected non-empty DownloadLink")
	}
	if loc.Checksum != "abc123" {
		t.Errorf("expected Checksum='abc123', got %q", loc.Checksum)
	}
	if loc.FolderID != "folder-456" {
		t.Errorf("expected FolderID='folder-456', got %q", loc.FolderID)
	}
	if loc.Action != finalization.PublishCreated {
		t.Errorf("expected Action=PublishCreated, got %q", loc.Action)
	}

	// Verify the PublishRequest was built correctly.
	req := stub.lastReq
	if req == nil {
		t.Fatal("expected Publish to be called")
	}
	if req.Destination != delivery.DestinationYouTubeClip {
		t.Errorf("expected Destination=DestinationYouTubeClip, got %q", req.Destination)
	}
	if req.LocalPath != localPath {
		t.Errorf("expected LocalPath=%q, got %q", localPath, req.LocalPath)
	}
	if req.Filename != "clip_abc123.mp4" {
		t.Errorf("expected Filename='clip_abc123.mp4', got %q", req.Filename)
	}
	if req.Description != "artifact test-artifact-001 v1 (video)" {
		t.Errorf("expected Description to carry the human-readable artifact summary, got %q", req.Description)
	}
	if req.AssetID != "test-artifact-001" {
		t.Errorf("expected AssetID='test-artifact-001', got %q", req.AssetID)
	}
	if req.ConflictPolicy != delivery.ConflictSkip {
		t.Errorf("expected ConflictPolicy=ConflictSkip (Drive-side dedup), got %v", req.ConflictPolicy)
	}
}

func TestArtifactPublisherAdapter_Publish_StockMetadata_UsesRunFingerprintPath(t *testing.T) {
	content := `{"ok":true}`
	localPath, sha := writeTempFile(t, content)

	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	artifact := finalization.VerifiedArtifact{
		ArtifactID:         "stock:1b25ac8e54701c88469a92e47cc32137415bbf8929da3c0dc5c1fbf3e8b54cb0:metadata",
		Kind:               finalization.KindMetadata,
		Filename:           "metadata.json",
		LocalPath:          localPath,
		MIMEType:           "application/json",
		SizeBytes:          int64(len(content)),
		SHA256:             sha,
		SourceVersion:      1,
		Requirement:        finalization.ArtifactRequirementRequired,
		IdempotencyKey:     "idem-key-stock-metadata",
		RootFolderName:     "Round_7_Broner_barcolla",
		RootFolderOverride: "drive-root-123",
	}

	_, err := adapter.Publish(context.Background(), artifact)
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	req := stub.lastReq
	if req == nil {
		t.Fatal("expected Publish to be called")
	}
	if req.Destination != delivery.DestinationStock {
		t.Fatalf("expected DestinationStock, got %q", req.Destination)
	}
	if req.Group != "Round_7_Broner_barcolla" {
		t.Fatalf("expected Group to use the readable run name, got %q", req.Group)
	}
	if req.Subject != "metadata" {
		t.Fatalf("expected Subject=metadata, got %q", req.Subject)
	}
	if req.Provider != "" {
		t.Fatalf("expected Provider empty, got %q", req.Provider)
	}
	if req.RootFolderOverride != "drive-root-123" {
		t.Fatalf("expected RootFolderOverride to pass through, got %q", req.RootFolderOverride)
	}
}

func TestArtifactPublisherAdapter_Publish_UnverifiedResolvedFolderFailsClosed(t *testing.T) {
	content := "unverified folder artifact"
	localPath, sha := writeTempFile(t, content)

	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	_, err := adapter.Publish(context.Background(), finalization.VerifiedArtifact{
		ArtifactID:       "stock:unverified-folder:video:0",
		Kind:             finalization.KindVideo,
		Filename:         "clip.mp4",
		LocalPath:        localPath,
		SHA256:           sha,
		Requirement:      finalization.ArtifactRequirementRequired,
		ResolvedFolderID: "unchecked-drive-folder",
	})
	if !errors.Is(err, ErrResolvedFolderNotVerified) {
		t.Fatalf("Publish() error = %v, want ErrResolvedFolderNotVerified", err)
	}
	if stub.lastReq != nil {
		t.Fatal("publisher must not be called for an unverified resolved folder")
	}
}

func TestArtifactPublisherAdapter_Publish_ExplicitTimestamp_UsesTimestampPath(t *testing.T) {
	content := `{"timestamp":true}`
	localPath, sha := writeTempFile(t, content)

	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	artifact := finalization.VerifiedArtifact{
		ArtifactID:         "stock:1b25ac8e54701c88469a92e47cc32137415bbf8929da3c0dc5c1fbf3e8b54cb0:timestamp:2:metadata",
		Kind:               finalization.KindMetadata,
		Filename:           "metadata.json",
		LocalPath:          localPath,
		MIMEType:           "application/json",
		SizeBytes:          int64(len(content)),
		SHA256:             sha,
		SourceVersion:      1,
		Requirement:        finalization.ArtifactRequirementRequired,
		IdempotencyKey:     "idem-key-explicit-timestamp",
		RootFolderName:     "Round_7_Broner_barcolla",
		RootFolderOverride: "drive-root-123",
		PathLeafName:       "timestamp_00-32_to_00-37_Round_7_Broner_barcolla",
	}

	_, err := adapter.Publish(context.Background(), artifact)
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	req := stub.lastReq
	if req == nil {
		t.Fatal("expected Publish to be called")
	}
	if req.Destination != delivery.DestinationStock {
		t.Fatalf("expected DestinationStock, got %q", req.Destination)
	}
	if req.Group != "Round_7_Broner_barcolla" {
		t.Fatalf("expected Group to use the readable run name, got %q", req.Group)
	}
	if req.Subject != "timestamp_00-32_to_00-37_Round_7_Broner_barcolla" {
		t.Fatalf("expected Subject to use the readable timestamp name, got %q", req.Subject)
	}
	if req.Provider != "" {
		t.Fatalf("expected Provider empty, got %q", req.Provider)
	}
	if req.RootFolderOverride != "drive-root-123" {
		t.Fatalf("expected RootFolderOverride to pass through, got %q", req.RootFolderOverride)
	}
}

// ── Test: Hash-mismatch rejection ───────────────────────────────────

func TestArtifactPublisherAdapter_Publish_HashMismatch(t *testing.T) {
	content := "original content"
	localPath, _ := writeTempFile(t, content)

	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	// Use a deliberately wrong SHA-256.
	artifact := finalization.VerifiedArtifact{
		ArtifactID:     "test-artifact-002",
		Kind:           finalization.KindImage,
		Filename:       "thumbnail.png",
		LocalPath:      localPath,
		MIMEType:       "image/png",
		SizeBytes:      int64(len(content)),
		SHA256:         "0000000000000000000000000000000000000000000000000000000000000000", // deliberately wrong
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementOptional,
		IdempotencyKey: "idem-key-002",
	}

	loc, err := adapter.Publish(context.Background(), artifact)

	// Should return ErrArtifactHashMismatch.
	if err == nil {
		t.Fatalf("expected hash-mismatch error, got nil (loc=%+v)", loc)
	}
	if !errors.Is(err, ErrArtifactHashMismatch) {
		t.Errorf("expected ErrArtifactHashMismatch, got: %v", err)
	}

	// Verify Publish was NOT called (hash gate fires first).
	if stub.lastReq != nil {
		t.Errorf("expected Publish NOT to be called on hash mismatch, but lastReq=%+v", stub.lastReq)
	}
}

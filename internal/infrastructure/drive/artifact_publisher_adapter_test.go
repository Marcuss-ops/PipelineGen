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
		ArtifactID:     "stock:1b25ac8e54701c88469a92e47cc32137415bbf8929da3c0dc5c1fbf3e8b54cb0:metadata",
		Kind:           finalization.KindMetadata,
		Filename:       "metadata.json",
		LocalPath:      localPath,
		MIMEType:       "application/json",
		SizeBytes:      int64(len(content)),
		SHA256:         sha,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "idem-key-stock-metadata",
		RootFolderName: "Round_7_Broner_barcolla",
		ParentFolderID: "drive-root-123",
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
	if req.ParentFolderID != "drive-root-123" {
		t.Fatalf("expected ParentFolderID to pass through, got %q", req.ParentFolderID)
	}
}

func TestArtifactPublisherAdapter_Publish_OverlayUsesExistingArtifactFolder(t *testing.T) {
	content := "overlay bytes"
	localPath, sha := writeTempFile(t, content)
	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	_, err := adapter.Publish(context.Background(), finalization.VerifiedArtifact{
		ArtifactID:         "video-847:overlay:001",
		Kind:               finalization.KindVideo,
		Filename:           "overlay_001.mov",
		LocalPath:          localPath,
		SHA256:             sha,
		SourceVersion:      4,
		Requirement:        finalization.ArtifactRequirementRequired,
		Source:             "chronon",
		ResolvedFolderID:   "artifact-folder-847",
		RootFolderResolved: true,
	})
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}
	if stub.lastReq == nil {
		t.Fatal("expected Publish to be called")
	}
	if stub.lastReq.DestinationFolderID != "artifact-folder-847" {
		t.Fatalf("overlay root = %q, want existing artifact folder", stub.lastReq.DestinationFolderID)
	}
	if len(stub.lastReq.DestinationSubpath) != 1 || stub.lastReq.DestinationSubpath[0] != "overlay" {
		t.Fatalf("overlay subpath = %#v, want [overlay]", stub.lastReq.DestinationSubpath)
	}
	if stub.lastReq.ConflictPolicy != delivery.ConflictSkip {
		t.Fatalf("overlay conflict policy = %v, want ConflictSkip", stub.lastReq.ConflictPolicy)
	}
}

func TestArtifactPublisherAdapter_Publish_OverlayExplicitSubpathWins(t *testing.T) {
	content := "overlay bytes"
	localPath, sha := writeTempFile(t, content)
	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	_, err := adapter.Publish(context.Background(), finalization.VerifiedArtifact{
		ArtifactID:         "video-847:overlay:002",
		Kind:               finalization.KindVideo,
		Filename:           "overlay_002.mov",
		LocalPath:          localPath,
		SHA256:             sha,
		Requirement:        finalization.ArtifactRequirementRequired,
		Source:             "chronon",
		DriveSubpath:       []string{"overlay", "v4"},
		ResolvedFolderID:   "artifact-folder-847",
		RootFolderResolved: true,
	})
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}
	if got := stub.lastReq.DestinationSubpath; len(got) != 2 || got[0] != "overlay" || got[1] != "v4" {
		t.Fatalf("explicit overlay subpath = %#v, want [overlay v4]", got)
	}
}

// TestArtifactPublisherAdapter_Publish_OverlayManifestFlow pins the last half
// of the probe→SHA256→manifest→publisher flow: a VerifiedArtifact projected
// from an overlay.render manifest (source=chronon + drive_subpath=[overlay] +
// probe sha256/size_bytes) publishes to Drive and returns the Drive identity
// (FileID → drive_file_id, WebViewLink → drive_link) with the overlay subpath
// preserved and the probe hash threaded as the publish ContentHash.
func TestArtifactPublisherAdapter_Publish_OverlayManifestFlow(t *testing.T) {
	content := "chronon overlay bytes"
	localPath, sha := writeTempFile(t, content)
	stub := &stubDeliveryPublisher{}
	adapter := NewArtifactPublisherAdapter(stub, nil)

	loc, err := adapter.Publish(context.Background(), finalization.VerifiedArtifact{
		ArtifactID:         "job_overlay:overlay:001",
		Kind:               finalization.KindVideo,
		Filename:           "overlay_001.mov",
		LocalPath:          localPath,
		MIMEType:           "video/quicktime",
		SizeBytes:          int64(len(content)),
		SHA256:             sha,
		SourceVersion:      1,
		Requirement:        finalization.ArtifactRequirementRequired,
		IdempotencyKey:     "job_overlay:overlay:001",
		Source:             "chronon",
		DriveSubpath:       []string{"overlay"},
		ResolvedFolderID:   "artifact-folder-847",
		RootFolderResolved: true,
	})
	if err != nil {
		t.Fatalf("Publish() unexpected error: %v", err)
	}

	// Drive identity comes back as the Location (drive_file_id / drive_link).
	if loc.Provider != "drive" {
		t.Fatalf("Provider = %q, want drive", loc.Provider)
	}
	if loc.FileID == "" {
		t.Fatal("Location.FileID (→ drive_file_id) is empty")
	}
	if loc.WebViewLink == "" {
		t.Fatal("Location.WebViewLink (→ drive_link) is empty")
	}
	if loc.FolderID == "" || loc.FolderPath == "" {
		t.Fatalf("Location folder identity incomplete: %+v", loc)
	}
	if loc.Action != finalization.PublishCreated {
		t.Fatalf("Action = %q, want PublishCreated", loc.Action)
	}

	// The overlay subpath + probe hash must survive to the Drive publisher.
	if got := stub.lastReq.DestinationSubpath; len(got) != 1 || got[0] != "overlay" {
		t.Fatalf("DestinationSubpath = %#v, want [overlay]", got)
	}
	if stub.lastReq.ContentHash != sha {
		t.Fatalf("ContentHash = %q, want probe sha256 %q", stub.lastReq.ContentHash, sha)
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
		ArtifactID:     "stock:1b25ac8e54701c88469a92e47cc32137415bbf8929da3c0dc5c1fbf3e8b54cb0:timestamp:2:metadata",
		Kind:           finalization.KindMetadata,
		Filename:       "metadata.json",
		LocalPath:      localPath,
		MIMEType:       "application/json",
		SizeBytes:      int64(len(content)),
		SHA256:         sha,
		SourceVersion:  1,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: "idem-key-explicit-timestamp",
		RootFolderName: "Round_7_Broner_barcolla",
		ParentFolderID: "drive-root-123",
		PathLeafName:   "timestamp_00-32_to_00-37_Round_7_Broner_barcolla",
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
	if req.ParentFolderID != "drive-root-123" {
		t.Fatalf("expected ParentFolderID to pass through, got %q", req.ParentFolderID)
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

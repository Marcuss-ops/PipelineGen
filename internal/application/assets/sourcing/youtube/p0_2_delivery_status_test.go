// Package youtube — P0.2 TDD tests for Drive-delivery status.
//
// Per the Drive Cutover Verdict §P0.2, YouTube registration must return
// an explicit delivery_status field instead of the pre-P0.2 ambiguous
// OK=true for both Drive-success and Drive-failure.
//
// Three canonical contracts tested:
//  1. Drive-OK → delivery_status=PUBLISHED
//  2. Drive-fail → delivery_status=PUBLISH_FAILED, retry_scheduled=true,
//     asset registered (OK=true, clipID non-empty)
//  3. RequireDrive=true + Drive-fail → error (ErrYouTubeDriveRequired)
package youtube

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Test doubles ──────────────────────────────────────────────────

type stubPublisher struct {
	publishFn func(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
}

func (s *stubPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	if s.publishFn != nil {
		return s.publishFn(ctx, req)
	}
	return &delivery.PublishResult{
		FileID:      "stub-file-id",
		WebViewLink: "https://drive.google.com/stub-link",
		FolderID:    "stub-folder",
	}, nil
}

type stubFetcher struct{}

func (s *stubFetcher) Fetch(ctx context.Context, req sourcing.FetchRequest) (*sourcing.FetchedAsset, error) {
	return &sourcing.FetchedAsset{
		LocalPath: "/tmp/stub-video.mp4",
		Name:      "Stub Video",
		Duration:  1e9, // 1 second
	}, nil
}

type stubIndexDispatcher struct{}

func (s *stubIndexDispatcher) EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error {
	return nil
}

type stubEnrichment struct {
	indexingEnabled bool
}

func (s *stubEnrichment) EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error {
	return nil
}

func (s *stubEnrichment) IndexingEnabled() bool { return s.indexingEnabled }
func (s *stubEnrichment) DispatchPostRegister(ctx context.Context, clipID, source, localPath string) error {
	return nil
}
func (s *stubEnrichment) SearchRelated(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
	return nil, nil
}
func (s *stubEnrichment) FolderDefaults() (string, string) { return "", "" }

type stubLogger struct{}

func (s *stubLogger) Info(msg string, kv ...any)  {}
func (s *stubLogger) Warn(msg string, kv ...any)  {}
func (s *stubLogger) Error(msg string, kv ...any) {}
func (s *stubLogger) Debug(msg string, kv ...any) {}

type stubTranscriber struct{}

func (s *stubTranscriber) Transcribe(ctx context.Context, audioPath string) (string, string, error) {
	return "test transcript text", "en", nil
}

type stubTextTrackRepo struct {
	tracks []asset.TextTrack
}

func (s *stubTextTrackRepo) UpsertBatch(ctx context.Context, tracks []asset.TextTrack) error {
	s.tracks = append(s.tracks, tracks...)
	return nil
}

func (s *stubTextTrackRepo) Find(ctx context.Context, assetID string, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) ListByAsset(ctx context.Context, assetID string) ([]asset.TextTrack, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) FindReady(ctx context.Context, assetID string, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, nil
}

func (s *stubTextTrackRepo) ListReadyLanguages(ctx context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) FindCurrentForTranslation(ctx context.Context, assetID string, kind asset.TextTrackKind, sourceTextHash string, targetLanguageCode string, translationModel string, modelVersion string, promptVersion string) (*asset.TextTrack, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) InsertTranslationWithAuditPredecessor(ctx context.Context, track asset.TextTrack) error {
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────

func newTestService(pub sourcing.PublisherPort, requireDrive bool) *Service {
	return &Service{
		fetcher:       &stubFetcher{},
		publisher:     pub,
		transcriber:   &stubTranscriber{},
		textTrackRepo: &stubTextTrackRepo{},
		indexDisp:     &stubIndexDispatcher{},
		enrichment:    &stubEnrichment{indexingEnabled: false},
		log:           &stubLogger{},
		requireDrive:  requireDrive,
	}
}

// ── Test 1: Drive-OK → PUBLISHED ──────────────────────────────────

func TestRegister_DriveOK_ReturnsPublished(t *testing.T) {
	pub := &stubPublisher{
		publishFn: func(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			return &delivery.PublishResult{
				FileID:      "drive-file-abc",
				WebViewLink: "https://drive.google.com/file/d/abc",
				FolderID:    "folder-123",
			}, nil
		},
	}
	svc := newTestService(pub, false)

	cmd := sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Test Clip",
	}

	result, err := svc.Register(context.Background(), cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.OK {
		t.Error("expected OK=true")
	}
	if result.DeliveryStatus != asset.AssetPublishPublished {
		t.Errorf("expected delivery_status=PUBLISHED, got %s", result.DeliveryStatus)
	}
	if result.DriveFileID == "" {
		t.Error("expected non-empty DriveFileID")
	}
	if result.DriveLink == "" {
		t.Error("expected non-empty DriveLink")
	}
	if result.RetryScheduled {
		t.Error("expected retry_scheduled=false for successful publish")
	}
}

// ── Test 2: Drive-fail → PUBLISH_FAILED, retry_scheduled, asset registered ──

func TestRegister_DriveFail_ReturnsPublishFailedWithAsset(t *testing.T) {
	pub := &stubPublisher{
		publishFn: func(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			return nil, errors.New("drive: network timeout")
		},
	}
	svc := newTestService(pub, false)

	cmd := sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Test Clip",
	}

	result, err := svc.Register(context.Background(), cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Asset is registered even though Drive failed.
	if !result.OK {
		t.Error("expected OK=true (asset registered locally)")
	}
	if result.ClipID == "" {
		t.Error("expected non-empty ClipID (asset was saved)")
	}

	// Delivery status is explicit.
	if result.DeliveryStatus != asset.AssetPublishFailed {
		t.Errorf("expected delivery_status=PUBLISH_FAILED, got %s", result.DeliveryStatus)
	}
	if !result.RetryScheduled {
		t.Error("expected retry_scheduled=true when Drive fails")
	}
	if result.DriveFileID != "" {
		t.Errorf("expected empty DriveFileID on failure, got %s", result.DriveFileID)
	}
}

// ── Test 3: RequireDrive=true + Drive-fail → error ─────────────

func TestRegister_RequireDrive_DriveFail_ReturnsError(t *testing.T) {
	pub := &stubPublisher{
		publishFn: func(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
			return nil, errors.New("drive: network timeout")
		},
	}
	svc := newTestService(pub, true) // RequireDrive=true

	cmd := sourcing.RegisterClipCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "Test Clip",
	}

	_, err := svc.Register(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected error when RequireDrive=true and Drive fails, got nil")
	}
	if !errors.Is(err, ErrYouTubeDriveRequired) {
		t.Errorf("expected ErrYouTubeDriveRequired, got %v", err)
	}
}

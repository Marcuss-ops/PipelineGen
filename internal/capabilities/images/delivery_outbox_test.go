package images

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"go.uber.org/zap"
)

type recordingImageDeliveryRepo struct {
	updates []imageDeliveryUpdate
	err     error
}

type imageDeliveryUpdate struct {
	hash, fileID, driveLink, downloadLink, status string
}

func (r *recordingImageDeliveryRepo) UpdateDriveDelivery(_ context.Context, hash, fileID, driveLink, downloadLink, status string) error {
	r.updates = append(r.updates, imageDeliveryUpdate{hash, fileID, driveLink, downloadLink, status})
	return r.err
}

type imageDeliveryPublisher struct {
	result *delivery.PublishResult
	err    error
	calls  int
}

func (p *imageDeliveryPublisher) Publish(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.calls++
	return p.result, p.err
}

func (p *imageDeliveryPublisher) ResolveFolder(context.Context, delivery.PublishRequest) (string, error) {
	return "", nil
}

func TestImageDriveDeliveryHandler_SuccessPersistsProjection(t *testing.T) {
	repo := &recordingImageDeliveryRepo{}
	publisher := &imageDeliveryPublisher{result: &delivery.PublishResult{
		FileID:       "drive-123",
		WebViewLink:  "https://drive.google.com/file/d/drive-123/view",
		DownloadLink: "https://drive.google.com/uc?id=drive-123",
	}}
	handler, err := NewImageDriveDeliveryHandler(repo, publisher, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	err = handler.HandlePayload(context.Background(), `{
		"asset_id":"asset-123","content_hash":"hash-123","local_path":"/tmp/image.jpg",
		"filename":"image.jpg","destination_folder_id":"images-root","source_version":1
	}`)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("publisher calls = %d, want 1", publisher.calls)
	}
	if len(repo.updates) != 1 {
		t.Fatalf("projection updates = %d, want 1", len(repo.updates))
	}
	got := repo.updates[0]
	if got.hash != "hash-123" || got.fileID != "drive-123" || got.driveLink == "" || got.downloadLink == "" || got.status != "ready" {
		t.Fatalf("projection = %#v, want successful Drive identity and ready status", got)
	}
}

func TestImageDriveDeliveryHandler_PublishFailureIsRetryableAndRecordsFailure(t *testing.T) {
	repo := &recordingImageDeliveryRepo{}
	publisher := &imageDeliveryPublisher{err: errors.New("Drive unavailable")}
	handler, err := NewImageDriveDeliveryHandler(repo, publisher, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	err = handler.HandlePayload(context.Background(), `{
		"asset_id":"asset-123","content_hash":"hash-123","local_path":"/tmp/image.jpg",
		"filename":"image.jpg","destination_folder_id":"images-root","source_version":1
	}`)
	if err == nil {
		t.Fatal("publish failure must be returned so the outbox retries")
	}
	if len(repo.updates) != 1 {
		t.Fatalf("failure projections = %d, want 1", len(repo.updates))
	}
	got := repo.updates[0]
	if got.fileID != "" || got.driveLink != "" || got.downloadLink != "" || got.status != "delivery_failed: Drive unavailable" {
		t.Fatalf("failure projection = %#v, want explicit retryable failure without Drive identity", got)
	}
}

func TestImageDriveDeliveryHandler_AllowsRegistryResolvedDestination(t *testing.T) {
	repo := &recordingImageDeliveryRepo{}
	publisher := &imageDeliveryPublisher{result: &delivery.PublishResult{FileID: "drive-123"}}
	handler, err := NewImageDriveDeliveryHandler(repo, publisher, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	err = handler.HandlePayload(context.Background(), `{
		"asset_id":"asset-123","content_hash":"hash-123","local_path":"/tmp/image.jpg"
	}`)
	if err != nil {
		t.Fatalf("registry-resolved destination should be accepted: %v", err)
	}
	if publisher.calls != 1 || len(repo.updates) != 1 {
		t.Fatalf("expected one post-commit publish and projection: publisher=%d updates=%d", publisher.calls, len(repo.updates))
	}
}

package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"go.uber.org/zap"
)

// EventTypeImageDriveDeliveryRequested is emitted atomically with the
// canonical media_assets ownership row. The worker is the only code path
// allowed to call Drive for this event.
const EventTypeImageDriveDeliveryRequested = "image.drive_delivery.requested"

// ImageDriveDeliveryPayload is the stable outbox payload for image delivery.
type ImageDriveDeliveryPayload struct {
	AssetID             string `json:"asset_id"`
	ContentHash         string `json:"content_hash"`
	LocalPath           string `json:"local_path"`
	Filename            string `json:"filename"`
	DestinationFolderID string `json:"destination_folder_id"`
	Style               string `json:"style,omitempty"`
	Subject             string `json:"subject,omitempty"`
	Group               string `json:"group,omitempty"`
	SourceVersion       int    `json:"source_version"`
}

// ImageDeliveryRepository is the worker's narrow post-publish projection port.
// It must not be used by the ingest request before the canonical commit.
type ImageDeliveryRepository interface {
	UpdateDriveDelivery(ctx context.Context, contentHash, driveFileID, driveLink, downloadLink, status string) error
}

var _ outboxevents.Handler = (*ImageDriveDeliveryHandler)(nil)

// ImageDriveDeliveryHandler performs Drive delivery after the ownership
// transaction has committed. A failed projection or publish is returned to
// the outbox pool so the event remains retryable.
type ImageDriveDeliveryHandler struct {
	repo      ImageDeliveryRepository
	publisher delivery.Publisher
	log       *zap.Logger
}

func NewImageDriveDeliveryHandler(repo ImageDeliveryRepository, publisher delivery.Publisher, log *zap.Logger) (*ImageDriveDeliveryHandler, error) {
	if repo == nil {
		return nil, errors.New("images.NewImageDriveDeliveryHandler: repository is required")
	}
	if publisher == nil {
		return nil, errors.New("images.NewImageDriveDeliveryHandler: publisher is required")
	}
	if log == nil {
		return nil, errors.New("images.NewImageDriveDeliveryHandler: logger is required")
	}
	return &ImageDriveDeliveryHandler{repo: repo, publisher: publisher, log: log}, nil
}

func (h *ImageDriveDeliveryHandler) EventType() string { return EventTypeImageDriveDeliveryRequested }
func (h *ImageDriveDeliveryHandler) IdempotencyKey() string {
	return EventTypeImageDriveDeliveryRequested + ".v1"
}

func (h *ImageDriveDeliveryHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	var payload ImageDriveDeliveryPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("image drive delivery: decode payload: %w", err)
	}
	if strings.TrimSpace(payload.AssetID) == "" || strings.TrimSpace(payload.ContentHash) == "" || strings.TrimSpace(payload.LocalPath) == "" {
		return fmt.Errorf("image drive delivery: asset_id, content_hash and local_path are required")
	}
	// An empty folder override is intentional: retrieved images use the
	// registry's canonical image destination. AI-generated images may carry
	// a style-specific override, but the worker never requires callers to
	// resolve a root folder synchronously.

	result, err := h.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationImage,
		DestinationFolderID: payload.DestinationFolderID,
		LocalPath:           payload.LocalPath,
		Filename:            payload.Filename,
		AssetID:             payload.AssetID,
		ContentHash:         payload.ContentHash,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationImage, payload.AssetID, payload.ContentHash, int64(payload.SourceVersion)),
		SourceVersion:       int64(payload.SourceVersion),
		Style:               payload.Style,
		Subject:             payload.Subject,
		Group:               payload.Group,
		ConflictPolicy:      delivery.ConflictSkip,
	})
	if err != nil {
		if projectionErr := h.markFailed(ctx, payload.ContentHash, err.Error()); projectionErr != nil {
			return fmt.Errorf("image drive delivery: publish: %w (record failure: %v)", err, projectionErr)
		}
		return fmt.Errorf("image drive delivery: publish: %w", err)
	}
	if result == nil || (strings.TrimSpace(result.FileID) == "" && strings.TrimSpace(result.WebViewLink) == "") {
		err := errors.New("publisher returned no Drive identity")
		if projectionErr := h.markFailed(ctx, payload.ContentHash, err.Error()); projectionErr != nil {
			return fmt.Errorf("image drive delivery: publish: %w (record failure: %v)", err, projectionErr)
		}
		return fmt.Errorf("image drive delivery: publish: %w", err)
	}

	driveLink := result.WebViewLink
	if driveLink == "" && result.FileID != "" {
		driveLink = "https://drive.google.com/file/d/" + result.FileID + "/view"
	}
	downloadLink := result.DownloadLink
	if downloadLink == "" && result.FileID != "" {
		downloadLink = "https://drive.google.com/uc?id=" + result.FileID
	}
	if err := h.repo.UpdateDriveDelivery(ctx, payload.ContentHash, result.FileID, driveLink, downloadLink, "ready"); err != nil {
		return fmt.Errorf("image drive delivery: persist success: %w", err)
	}
	h.log.Info("image Drive delivery completed", zap.String("asset_id", payload.AssetID), zap.String("drive_file_id", result.FileID))
	return nil
}

func (h *ImageDriveDeliveryHandler) markFailed(ctx context.Context, hash, message string) error {
	if err := h.repo.UpdateDriveDelivery(ctx, hash, "", "", "", "delivery_failed: "+message); err != nil {
		h.log.Warn("image Drive delivery failure projection failed", zap.Error(err), zap.String("content_hash", hash))
		return err
	}
	return nil
}

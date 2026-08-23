package cliprender

// delivery_outbox.go defines the clip.drive_delivery.requested outbox event,
// its stable payload, and the handler port consumed by the outbox worker.
//
// Flow:
//
//	clip.render worker
//	  → hashes output, resolves taxonomy
//	  → commits media_assets row + clip.drive_delivery.requested outbox event
//	    (single atomic SQLite transaction — Drive is NOT called here)
//	  → returns success (asset_id, no Drive link yet)
//
//	outbox worker (ClipDriveDeliveryHandler)
//	  → decodes ClipDriveDeliveryPayload
//	  → uploads video to Drive via delivery.Publisher
//	  → uploads sidecar ASS (when present)
//	  → updates media_assets projection (drive_file_id, drive_link, status)
//	  → marks delivery as "ready"
//
// This mirrors the image.drive_delivery pattern (capabilities/images/delivery_outbox.go):
// Drive is always post-commit, durable, and retryable.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"go.uber.org/zap"
)

// EventTypeClipDriveDeliveryRequested is emitted atomically with the
// canonical media_assets row. The worker is the only code path allowed
// to call Drive for this event.
const EventTypeClipDriveDeliveryRequested = "clip.drive_delivery.requested"

// ClipDriveDeliveryPayload is the stable outbox payload for clip delivery.
type ClipDriveDeliveryPayload struct {
	AssetID             string `json:"asset_id"`
	ContentHash         string `json:"content_hash"`
	LocalPath           string `json:"local_path"`
	Filename            string `json:"filename"`
	DestinationFolderID string `json:"destination_folder_id"`
	SourceVersion       int    `json:"source_version"`

	// Sidecar fields (optional — only present when subtitles were compiled).
	SidecarLocalPath string `json:"sidecar_local_path,omitempty"`
	SidecarSHA256    string `json:"sidecar_sha256,omitempty"`
	SidecarFilename  string `json:"sidecar_filename,omitempty"`

	// Metadata for projection.
	SourceAssetID string `json:"source_asset_id"`
	RunID         string `json:"run_id"`
	SizeBytes     int64  `json:"size_bytes"`
}

// ClipDeliveryRepository is the worker's narrow post-publish projection port.
// It must not be used by the request handler before the canonical commit.
type ClipDeliveryRepository interface {
	UpdateClipDriveDelivery(ctx context.Context, assetID, contentHash, driveFileID, driveLink, downloadLink, sidecarFileID, sidecarLink, status string) error
}

// ClipDriveDeliveryHandler performs Drive delivery after the ownership
// transaction has committed. A failed projection or publish is returned to
// the outbox pool so the event remains retryable.
type ClipDriveDeliveryHandler struct {
	repo      ClipDeliveryRepository
	publisher delivery.Publisher
	log       *zap.Logger
}

// NewClipDriveDeliveryHandler builds the handler. Fail-closed: nil repo,
// publisher, or log are typed errors at construction time.
func NewClipDriveDeliveryHandler(repo ClipDeliveryRepository, publisher delivery.Publisher, log *zap.Logger) (*ClipDriveDeliveryHandler, error) {
	if repo == nil {
		return nil, errors.New("cliprender.NewClipDriveDeliveryHandler: repository is required")
	}
	if publisher == nil {
		return nil, errors.New("cliprender.NewClipDriveDeliveryHandler: publisher is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipDriveDeliveryHandler{repo: repo, publisher: publisher, log: log}, nil
}

// EventType returns the canonical event type this handler consumes.
func (h *ClipDriveDeliveryHandler) EventType() string { return EventTypeClipDriveDeliveryRequested }

// IdempotencyKey returns the handler's stable identifier.
func (h *ClipDriveDeliveryHandler) IdempotencyKey() string {
	return EventTypeClipDriveDeliveryRequested + ".v1"
}

// HandlePayload consumes the JSON payload from a committed outbox event.
func (h *ClipDriveDeliveryHandler) HandlePayload(ctx context.Context, payloadJSON string) error {
	var payload ClipDriveDeliveryPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return fmt.Errorf("clip drive delivery: decode payload: %w", err)
	}
	if strings.TrimSpace(payload.AssetID) == "" || strings.TrimSpace(payload.LocalPath) == "" || strings.TrimSpace(payload.ContentHash) == "" {
		return fmt.Errorf("clip drive delivery: asset_id, local_path and content_hash are required")
	}

	// Upload the rendered MP4.
	result, err := h.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationRenderedClip,
		DestinationFolderID: payload.DestinationFolderID,
		LocalPath:           payload.LocalPath,
		Filename:            payload.Filename,
		AssetID:             payload.AssetID,
		ContentHash:         payload.ContentHash,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationRenderedClip, payload.AssetID, payload.ContentHash, int64(payload.SourceVersion)),
		SourceVersion:       int64(payload.SourceVersion),
		ConflictPolicy:      delivery.ConflictSkip,
	})
	if err != nil {
		_ = h.markFailed(ctx, payload.AssetID, payload.ContentHash, err.Error())
		return fmt.Errorf("clip drive delivery: publish video: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		err := errors.New("publisher returned no Drive identity for video")
		_ = h.markFailed(ctx, payload.AssetID, payload.ContentHash, err.Error())
		return fmt.Errorf("clip drive delivery: publish video: %w", err)
	}

	// Upload the sidecar ASS when present.
	var sidecarFileID, sidecarLink string
	if strings.TrimSpace(payload.SidecarLocalPath) != "" && strings.TrimSpace(payload.SidecarSHA256) != "" {
		sidecarFilename := payload.SidecarFilename
		if sidecarFilename == "" {
			sidecarFilename = payload.AssetID + ".ass"
		}
		sidecarResult, sidecarErr := h.publisher.Publish(ctx, delivery.PublishRequest{
			Destination:         delivery.DestinationClipMetadata,
			DestinationFolderID: payload.DestinationFolderID,
			LocalPath:           payload.SidecarLocalPath,
			Filename:            sidecarFilename,
			AssetID:             payload.AssetID,
			ContentHash:         payload.SidecarSHA256,
			IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, payload.AssetID, payload.SidecarSHA256, 1),
			SourceVersion:       1,
			ConflictPolicy:      delivery.ConflictOverwrite,
		})
		if sidecarErr != nil {
			h.log.Warn("clip drive delivery: sidecar upload failed (video uploaded; sidecar will retry)", zap.Error(sidecarErr))
		} else if sidecarResult != nil {
			sidecarFileID = sidecarResult.FileID
			sidecarLink = sidecarResult.WebViewLink
		}
	}

	driveLink := result.WebViewLink
	if driveLink == "" && result.FileID != "" {
		driveLink = "https://drive.google.com/file/d/" + result.FileID + "/view"
	}
	downloadLink := result.DownloadLink
	if downloadLink == "" && result.FileID != "" {
		downloadLink = "https://drive.google.com/uc?id=" + result.FileID
	}
	if err := h.repo.UpdateClipDriveDelivery(ctx, payload.AssetID, payload.ContentHash, result.FileID, driveLink, downloadLink, sidecarFileID, sidecarLink, "ready"); err != nil {
		return fmt.Errorf("clip drive delivery: persist success: %w", err)
	}
	h.log.Info("clip Drive delivery completed",
		zap.String("asset_id", payload.AssetID),
		zap.String("drive_file_id", result.FileID),
	)
	return nil
}

func (h *ClipDriveDeliveryHandler) markFailed(ctx context.Context, assetID, contentHash, message string) error {
	if err := h.repo.UpdateClipDriveDelivery(ctx, assetID, contentHash, "", "", "", "", "", "delivery_failed: "+message); err != nil {
		h.log.Warn("clip Drive delivery failure projection failed", zap.Error(err), zap.String("asset_id", assetID))
		return err
	}
	return nil
}

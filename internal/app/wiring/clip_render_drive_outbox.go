package wiring

// clip_render_drive_outbox.go owns the composition-root consumer for the
// clip.render Drive projection. The render handler commits the asset and this
// event atomically; this consumer performs the slow external upload and then
// reconciles the canonical Drive location.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

var errClipRenderDrivePayload = errors.New("clip.render drive delivery: invalid payload")

type clipRenderDriveDeliveryHandler struct {
	publisher delivery.Publisher
	mutator   persistence.AssetMutator
	log       *zap.Logger
}

func newClipRenderDriveDeliveryHandler(publisher delivery.Publisher, mutator persistence.AssetMutator, log *zap.Logger) (*clipRenderDriveDeliveryHandler, error) {
	if publisher == nil {
		return nil, fmt.Errorf("clip.render Drive delivery: publisher is required")
	}
	if mutator == nil {
		return nil, fmt.Errorf("clip.render Drive delivery: asset mutator is required")
	}
	if log == nil {
		return nil, fmt.Errorf("clip.render Drive delivery: logger is required")
	}
	return &clipRenderDriveDeliveryHandler{publisher: publisher, mutator: mutator, log: log}, nil
}

func (h *clipRenderDriveDeliveryHandler) EventType() string {
	return cliprender.EventClipRenderDriveDeliveryRequested
}

func (h *clipRenderDriveDeliveryHandler) IdempotencyKey() string {
	return cliprender.EventClipRenderDriveDeliveryRequested
}

func (h *clipRenderDriveDeliveryHandler) Handle(ctx context.Context, claim *pgmedia.OutboxClaim) error {
	if claim == nil {
		return fmt.Errorf("%w: nil outbox claim", errClipRenderDrivePayload)
	}
	evt := claim.Event
	var req cliprender.ClipRenderDriveDeliveryRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		return fmt.Errorf("%w: json: %v", errClipRenderDrivePayload, err)
	}
	if req.SchemaVersion != "clip.render.drive_delivery.v1" ||
		strings.TrimSpace(req.AssetID) == "" || strings.TrimSpace(req.LocalPath) == "" ||
		strings.TrimSpace(req.Filename) == "" || strings.TrimSpace(req.FolderID) == "" ||
		strings.TrimSpace(req.ContentHash) == "" || req.SizeBytes <= 0 {
		return fmt.Errorf("%w: missing schema/asset/path/name/folder/hash/size", errClipRenderDrivePayload)
	}
	info, err := os.Stat(req.LocalPath)
	if err != nil {
		return fmt.Errorf("clip.render Drive upload %s: staged artifact unavailable: %w", req.AssetID, err)
	}
	if info.Size() != req.SizeBytes {
		return fmt.Errorf("clip.render Drive upload %s: local size=%d want=%d", req.AssetID, info.Size(), req.SizeBytes)
	}
	result, err := h.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: req.FolderID,
		LocalPath:           req.LocalPath,
		Filename:            req.Filename,
		AssetID:             req.AssetID,
		ProjectID:           req.RunID,
		SizeBytes:           req.SizeBytes,
		ContentHash:         req.ContentHash,
		SourceVersion:       1,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, req.AssetID, req.ContentHash, 1),
		ConflictPolicy:      delivery.ConflictOverwrite,
	})
	if err != nil {
		return fmt.Errorf("clip.render Drive upload %s: %w", req.AssetID, err)
	}
	if result == nil || result.FileID == "" {
		return fmt.Errorf("%w: Drive returned no file id for %s", errClipRenderDrivePayload, req.AssetID)
	}
	if err := h.mutator.ReconcileDriveLocations(ctx, []persistence.DriveLocationPatch{{
		AssetID: req.AssetID, DriveFileID: result.FileID, DriveLink: result.WebViewLink,
		DownloadURL: result.DownloadLink,
	}}); err != nil {
		return fmt.Errorf("clip.render Drive reconcile %s: %w", req.AssetID, err)
	}
	if err := os.Remove(req.LocalPath); err != nil && !os.IsNotExist(err) {
		h.log.Warn("clip.render Drive delivery completed but local artifact cleanup failed",
			zap.String("asset_id", req.AssetID), zap.String("path", req.LocalPath), zap.Error(err))
	}
	h.log.Info("clip.render Drive delivery completed",
		zap.String("asset_id", req.AssetID), zap.String("drive_file_id", result.FileID),
		zap.String("event_key", evt.EventKey), zap.Int64("size_bytes", req.SizeBytes))
	return nil
}

// imageDriveDeliveryPGHandler adapts the capability-owned image payload
// handler to the PostgreSQL media outbox worker. Image delivery intents are
// emitted by the same Postgres AssetCommitter as clip intents.
type imageDriveDeliveryPGHandler struct {
	handler *imagesapp.ImageDriveDeliveryHandler
}

func (h imageDriveDeliveryPGHandler) Handle(ctx context.Context, claim *pgmedia.OutboxClaim) error {
	if claim == nil {
		return fmt.Errorf("image Drive delivery: nil outbox claim")
	}
	return h.handler.HandlePayload(ctx, claim.Event.PayloadJSON)
}

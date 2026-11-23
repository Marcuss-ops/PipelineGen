package assets

import (
	"context"
	"fmt"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// NewImageDriveDeliveryPGHandler builds the image delivery adapter for the
// PostgreSQL media outbox. The adapter and its repository bridge live with the
// asset wiring family; the root composition only registers the handler.
func NewImageDriveDeliveryPGHandler(mutator assetspersistence.AssetMutator, publisher delivery.Publisher, log *zap.Logger) (pgmedia.OutboxHandler, error) {
	writer, ok := mutator.(assetspersistence.AssetDriveDeliveryWriter)
	if !ok || writer == nil {
		return nil, fmt.Errorf("image PostgreSQL Drive delivery: canonical asset mutator does not support Drive delivery by asset id")
	}
	handler, err := imagesapp.NewImageDriveDeliveryHandler(imageDriveDeliveryRepository{writer: writer}, publisher, log)
	if err != nil {
		return nil, err
	}
	return imageDriveDeliveryPGHandler{handler: handler}, nil
}

type imageDriveDeliveryRepository struct {
	writer assetspersistence.AssetDriveDeliveryWriter
}

func (r imageDriveDeliveryRepository) UpdateDriveDelivery(ctx context.Context, assetID, driveFileID, driveLink, downloadLink, status string) error {
	if r.writer == nil {
		return fmt.Errorf("image Drive delivery: canonical asset writer is unavailable")
	}
	return r.writer.UpdateDriveDeliveryByAssetID(ctx, assetID, assetspersistence.DriveDeliveryMutation{
		DriveFileID:  driveFileID,
		DriveLink:    driveLink,
		DownloadLink: downloadLink,
		Status:       status,
	})
}

type imageDriveDeliveryPGHandler struct {
	handler *imagesapp.ImageDriveDeliveryHandler
}

func (h imageDriveDeliveryPGHandler) Handle(ctx context.Context, claim *pgmedia.OutboxClaim) error {
	if claim == nil {
		return fmt.Errorf("image Drive delivery: nil outbox claim")
	}
	if h.handler == nil {
		return fmt.Errorf("image Drive delivery: handler is unavailable")
	}
	return h.handler.HandlePayload(ctx, claim.Event.PayloadJSON)
}

var _ pgmedia.OutboxHandler = imageDriveDeliveryPGHandler{}

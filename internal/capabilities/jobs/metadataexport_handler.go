// Package outbox contains the technical adapter for metadata export events.
//
// The metadata export capability itself lives in
// internal/capabilities/assets/metadataexport. This file is deliberately
// limited to the outboxevents.Handler contract: decode and validate the
// event, resolve its scope through the capability port, then dispatch to
// the capability service. It is kept here because outboxevents is a
// technical transport concern, not an asset capability concern.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	assetmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/metadataexport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// MetadataExportRequestSchemaVersion is retained at the outbox boundary as
// the handler's public schema marker. The canonical request contract lives in
// the metadataexport capability package.
const MetadataExportRequestSchemaVersion = assetmetadata.MetadataExportRequestSchemaVersion

// MetadataExportHandler is the technical outbox adapter for
// asset.metadata_export.requested.v1. It owns no SQL or filesystem logic.
type MetadataExportHandler struct {
	log      *zap.Logger
	service  *assetmetadata.Service
	resolver assetmetadata.AssetResolver
}

// MetadataExportHandlerDeps contains the typed capability ports needed by the
// outbox adapter. Infrastructure adapters are created by the composition root.
type MetadataExportHandlerDeps struct {
	Resolver  assetmetadata.AssetResolver
	Writer    assetmetadata.ExportWriter
	OutputDir string
	Log       *zap.Logger
}

// NewMetadataExportHandler constructs the technical outbox adapter.
func NewMetadataExportHandler(deps MetadataExportHandlerDeps) *MetadataExportHandler {
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &MetadataExportHandler{
		log:      log,
		service:  assetmetadata.NewService(log, deps.Resolver, deps.Writer, deps.OutputDir),
		resolver: deps.Resolver,
	}
}

func (h *MetadataExportHandler) EventType() string {
	return outboxevents.EventAssetMetadataExportRequested
}

func (h *MetadataExportHandler) IdempotencyKey() string {
	return outboxevents.EventAssetMetadataExportRequested + "." + MetadataExportRequestSchemaVersion
}

// Handle processes one durable outbox event. Envelope failures are terminal;
// resolver/service failures are returned unchanged so the outbox retry policy
// can classify them correctly.
func (h *MetadataExportHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	var req assetmetadata.MetadataExportRequest
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		log.Warn("asset.metadata_export.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID), zap.Error(err))
		return fmt.Errorf("%w: parse: %s", assetmetadata.ErrMetadataTerminal, err.Error())
	}
	if err := assetmetadata.Validate(&req); err != nil {
		log.Warn("asset.metadata_export.requested envelope validation failed",
			zap.Int64("event_id", evt.ID), zap.Error(err))
		return err
	}

	ids, err := h.resolveAssetIDs(ctx, &req)
	if err != nil {
		log.Warn("asset.metadata_export.requested asset_id resolution failed",
			zap.Int64("event_id", evt.ID), zap.Error(err))
		return fmt.Errorf("asset.metadata_export.requested resolve asset_ids: %w", err)
	}
	if len(ids) == 0 {
		log.Info("asset.metadata_export.requested: no assets resolved — completed with empty result",
			zap.String("job_id", req.JobID), zap.Int64("event_id", evt.ID))
		return nil
	}

	switch req.Destination.Provider {
	case assetmetadata.DestinationDrive:
		log.Info("asset.metadata_export.requested acknowledged — drive upload handled by upload pipeline",
			zap.String("folder_id", req.Destination.FolderID),
			zap.Int("asset_count", len(ids)),
			zap.String("idempotency_key", req.IdempotencyKey),
			zap.Int64("event_id", evt.ID))
		return nil
	case assetmetadata.DestinationFilesystem:
		return h.service.ExportFilesystem(ctx, evt.ID, &req, ids)
	default:
		return fmt.Errorf("%w: destination.provider=%q unknown", assetmetadata.ErrMetadataTerminal, req.Destination.Provider)
	}
}

func (h *MetadataExportHandler) resolveAssetIDs(ctx context.Context, req *assetmetadata.MetadataExportRequest) ([]string, error) {
	if len(req.AssetIDs) > 0 {
		return req.AssetIDs, nil
	}
	if req.JobID == "" || h.resolver == nil {
		return nil, nil
	}
	return h.resolver.ResolveAssetIDs(ctx, req.JobID, nil)
}

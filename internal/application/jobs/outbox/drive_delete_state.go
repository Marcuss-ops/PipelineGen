package outbox

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// preflightDriveDelete validates that the asset is at a state owned by this
// handler. skip=true represents the existing idempotent-success paths.
func (h *DriveDeleteHandler) preflightDriveDelete(
	ctx context.Context,
	req driveDeleteRequestV1,
	reqLog []zap.Field,
	log *zap.Logger,
) (*asset.Asset, bool, error) {
	if h.stateReader == nil {
		return nil, false, errors.New("drive_delete: stateReader not wired (production wiring must supply *assets.ClipsRepository)")
	}

	clip, err := h.stateReader.GetClip(ctx, req.AssetID)
	if err != nil {
		log.Warn("drive_delete: pre-flight GetClip failed (retryable)", append(reqLog, zap.Error(err))...)
		return nil, false, fmt.Errorf("drive_delete GetClip(%s): %w", req.AssetID, err)
	}
	if clip == nil {
		log.Info("drive_delete: asset row absent — idempotent skip", reqLog...)
		return nil, true, nil
	}

	switch string(clip.LifecycleState) {
	case string(asset.StateLifecycleIndexDeletePending), string(asset.StateDeleted), "deleted":
		log.Info("drive_delete: already past Drive hop — idempotent skip",
			append(reqLog, zap.String("lifecycle_state", string(clip.LifecycleState)))...,
		)
		return clip, true, nil
	case string(asset.StateDeleteRequested),
		string(asset.StateDeletePending),
		string(asset.StateDriveDeletePending):
		return clip, false, nil
	default:
		log.Warn("drive_delete: asset in unexpected lifecycle_state — terminal",
			append(reqLog, zap.String("lifecycle_state", string(clip.LifecycleState)))...,
		)
		return nil, false, terminalWrap(fmt.Errorf(
			"%w: unexpected lifecycle_state %q for %s",
			driveLifecycleTerminalErr,
			clip.LifecycleState,
			req.AssetID,
		))
	}
}

func (h *DriveDeleteHandler) stampDriveDeletePending(
	ctx context.Context,
	req driveDeleteRequestV1,
	reqLog []zap.Field,
	log *zap.Logger,
) error {
	log.Info("drive_delete: stamping DRIVE_DELETE_PENDING", reqLog...)
	if err := h.stateWriter.SetLifecycleState(ctx, req.AssetID, asset.StateDriveDeletePending); err != nil {
		log.Warn("drive_delete: SetLifecycleState(DRIVE_DELETE_PENDING) failed (retryable)",
			append(reqLog, zap.Error(err))...,
		)
		return fmt.Errorf("drive_delete SetLifecycleState(DRIVE_DELETE_PENDING, %s): %w", req.AssetID, err)
	}
	return nil
}

func (h *DriveDeleteHandler) advanceDriveDelete(
	ctx context.Context,
	req driveDeleteRequestV1,
	reqLog []zap.Field,
	log *zap.Logger,
) error {
	nextPayload, err := buildIndexDeletePayloadForDrive(req.AssetID)
	if err != nil {
		return fmt.Errorf("drive_delete build index-delete payload: %w", err)
	}

	nextEventKey := "delete:" + req.AssetID
	log.Info("drive_delete: advancing to DRIVE_DELETED + emitting index.delete_requested event",
		append(reqLog, zap.String("next_event_type", outboxevents.EventAssetIndexDeleteRequested))...,
	)
	if err := h.advancer.AdvanceAndEmit(
		ctx,
		req.AssetID,
		asset.StateDriveDeletePending,
		asset.StateDriveDeleted,
		outboxevents.EventAssetIndexDeleteRequested,
		nextPayload,
		nextEventKey,
	); err != nil {
		log.Warn("drive_delete: AdvanceAndEmit failed (retryable)", append(reqLog, zap.Error(err))...)
		return fmt.Errorf("drive_delete AdvanceAndEmit(%s): %w", req.AssetID, err)
	}
	return nil
}

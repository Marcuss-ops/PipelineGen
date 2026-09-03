// Package jobs contains the application-layer handlers for outbox events.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// DriveDeleteEventType is the outbox event consumed by DriveDeleteHandler.
const DriveDeleteEventType = "asset.drive.delete_requested"

// DriveDeleteRequestSchemaVersion is the exact envelope version accepted by the
// handler. Schema drift is terminal rather than retryable.
const DriveDeleteRequestSchemaVersion = "asset.drive.delete_requested.v1"

type driveDeleteRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Permanently    bool   `json:"permanently,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

var driveLifecycleTerminalErr = errors.New("drive_delete: terminal envelope error")

func terminalWrap(err error) error {
	return outboxevents.NewTerminalError(err)
}

// DriveDeleteHandler advances the deletion state machine through the Drive hop.
// Input validation, lifecycle transitions and Drive operations stay separated
// by helper ownership while sharing this package-level handler surface.
type DriveDeleteHandler struct {
	log         *zap.Logger
	drive       DriveDeleter
	stateReader LifecycleStateReader
	stateWriter ClipsLifecycleStateWriter
	advancer    StateAdvancer
}

// NewDriveDeleteHandler wires the handler's narrow ports.
func NewDriveDeleteHandler(
	log *zap.Logger,
	drive DriveDeleter,
	stateReader LifecycleStateReader,
	stateWriter ClipsLifecycleStateWriter,
	advancer StateAdvancer,
) *DriveDeleteHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveDeleteHandler{
		log:         log.Named("drive_delete"),
		drive:       drive,
		stateReader: stateReader,
		stateWriter: stateWriter,
		advancer:    advancer,
	}
}

// EventType returns the canonical outbox event type.
func (h *DriveDeleteHandler) EventType() string {
	return DriveDeleteEventType
}

// IdempotencyKey is the static handler-class identifier. Per-event deduplication
// remains owned by outbox_events.event_key and the request envelope.
func (h *DriveDeleteHandler) IdempotencyKey() string {
	return DriveDeleteEventType + "." + DriveDeleteRequestSchemaVersion
}

// parseDriveDeleteRequest owns JSON decoding, strict envelope validation and
// the canonical structured fields shared by the remaining handler stages.
func parseDriveDeleteRequest(
	evt outboxevents.Event,
	log *zap.Logger,
) (driveDeleteRequestV1, []zap.Field, error) {
	var req driveDeleteRequestV1
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		log.Warn("drive_delete payload parse failed",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Error(err),
		)
		return driveDeleteRequestV1{}, nil, terminalWrap(fmt.Errorf("%w: %v", driveLifecycleTerminalErr, err))
	}

	if req.SchemaVersion != DriveDeleteRequestSchemaVersion {
		log.Warn("drive_delete schema_version mismatch",
			zap.Int64("event_id", evt.ID),
			zap.String("got_version", req.SchemaVersion),
			zap.String("want_version", DriveDeleteRequestSchemaVersion),
		)
		return driveDeleteRequestV1{}, nil, terminalWrap(fmt.Errorf(
			"%w: schema_version %q != %q",
			driveLifecycleTerminalErr,
			req.SchemaVersion,
			DriveDeleteRequestSchemaVersion,
		))
	}
	if req.AssetID == "" {
		log.Warn("drive_delete: empty asset_id", zap.Int64("event_id", evt.ID))
		return driveDeleteRequestV1{}, nil, terminalWrap(fmt.Errorf("%w: asset_id is required", driveLifecycleTerminalErr))
	}
	if req.IdempotencyKey == "" {
		log.Warn("drive_delete: missing idempotency_key",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", req.AssetID),
		)
		return driveDeleteRequestV1{}, nil, terminalWrap(fmt.Errorf("%w: idempotency_key is required", driveLifecycleTerminalErr))
	}

	reqLog := []zap.Field{
		zap.String("asset_id", req.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.String("outbox_event_id", req.EventID),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Int("attempt", evt.AttemptCount),
		zap.Bool("permanently", req.Permanently),
	}
	return req, reqLog, nil
}

// Handle preserves the canonical sequence:
// parse/validate -> preflight -> DRIVE_DELETE_PENDING -> Drive side effect ->
// DRIVE_DELETED + index-delete event.
func (h *DriveDeleteHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	req, reqLog, err := parseDriveDeleteRequest(evt, log)
	if err != nil {
		return err
	}

	clip, skip, err := h.preflightDriveDelete(ctx, req, reqLog, log)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	if err := h.stampDriveDeletePending(ctx, req, reqLog, log); err != nil {
		return err
	}
	if err := h.performDriveDelete(ctx, clip, req, reqLog, log); err != nil {
		return err
	}
	if err := h.advanceDriveDelete(ctx, req, reqLog, log); err != nil {
		return err
	}

	log.Info("drive_delete: deletion complete", reqLog...)
	return nil
}

var _ outboxevents.Handler = (*DriveDeleteHandler)(nil)
var _ = outboxevents.EventAssetDriveDeleteRequested == DriveDeleteEventType

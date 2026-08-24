// Package outbox contains the application-layer handlers for outbox events.
package jobs

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// DriveDeleteEventType is the outbox event consumed by DriveDeleteHandler.
const DriveDeleteEventType = "asset.drive.delete_requested"

// DriveDeleteHandler advances the deletion state machine through the Drive hop.
// Envelope validation, lifecycle transitions, Drive operations and next-event
// construction live in focused sibling files in this package.
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

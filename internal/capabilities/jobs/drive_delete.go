// Package jobs contains the application-layer handlers for outbox events.
package jobs

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// DriveDeleteEventType is the outbox event consumed by DriveDeleteHandler.
const DriveDeleteEventType = event.AssetDriveDeleteRequested

// DriveDeleteRequestSchemaVersion is the exact envelope version accepted by the
// handler. Schema drift is terminal rather than retryable.
const DriveDeleteRequestSchemaVersion = event.AssetDriveDeleteRequestedV1Schema

type driveDeleteRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Permanently    bool   `json:"permanently,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

var errDriveLifecycleTerminal = errors.New("drive_delete: terminal envelope error")

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
	if err := outboxevents.DecodeV1(outboxevents.DecodePolicy{
		EventName:         "drive_delete",
		WantSchemaVersion: DriveDeleteRequestSchemaVersion,
		Log:               log,
		OnViolation:       driveDeleteViolation,
	}, evt, &req); err != nil {
		return driveDeleteRequestV1{}, nil, err
	}
	if req.AssetID == "" {
		log.Warn("drive_delete: empty asset_id", zap.Int64("event_id", evt.ID))
		return driveDeleteRequestV1{}, nil, terminalWrap(fmt.Errorf("%w: asset_id is required", errDriveLifecycleTerminal))
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

// driveDeleteViolation keeps the handler's single terminal sentinel
// (errDriveLifecycleTerminal) while the envelope CHECKS live in
// outboxevents.DecodeV1.
func driveDeleteViolation(v outboxevents.Violation) error {
	switch v.Kind {
	case outboxevents.ViolationParse:
		return terminalWrap(fmt.Errorf("%w: %v", errDriveLifecycleTerminal, v.Err))
	case outboxevents.ViolationSchemaVersion:
		return terminalWrap(fmt.Errorf("%w: schema_version %q != %q", errDriveLifecycleTerminal, v.Got, v.Want))
	case outboxevents.ViolationIdempotencyKey:
		return terminalWrap(fmt.Errorf("%w: idempotency_key is required", errDriveLifecycleTerminal))
	default:
		return terminalWrap(fmt.Errorf("%w: %s envelope violation", errDriveLifecycleTerminal, v.Kind))
	}
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

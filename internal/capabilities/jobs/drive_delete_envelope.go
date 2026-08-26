package jobs

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

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

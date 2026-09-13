// Package jobs — index_restore.go: the consumer half of the restore saga.
//
// The producer (mutations.AssetMutationDispatcher.EnqueueAndRestore →
// pgmedia.PostgresMediaCommitter.EnqueueAndRestore) atomically stamps the row's
// index_state to DISCOVERED and emits asset.index.restore_requested with the
// documented contract "outbox handler re-indexes from scratch". Until this
// handler existed, that event had NO consumer on either engine, so every
// restore silently dead-lettered and the asset never came back into the index.
//
// This handler closes the hop the same way IndexDeleteHandler closes the delete
// hop: strict envelope validation (terminal on anything retrying cannot fix),
// then one call into the narrow restore port, which re-emits the canonical
// asset.index.requested envelope so the index plane rebuilds the projection.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// IndexRestoreEventType is the outbox event consumed by IndexRestoreHandler.
const IndexRestoreEventType = event.AssetIndexRestoreRequested

// IndexRestoreRequestSchemaVersion is the exact envelope version accepted by
// the handler. Schema drift is terminal rather than retryable.
const IndexRestoreRequestSchemaVersion = event.AssetIndexRestoreRequestedV1Schema

var indexRestoreTerminalErr = errors.New("index_restore: terminal envelope error")

// indexRestoreRequestV1 is the canonical envelope for
// asset.index.restore_requested.v1 events. It mirrors the producer's payload
// field-for-field (pgmedia.PostgresMediaCommitter.EnqueueAndRestore).
type indexRestoreRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Operation      string `json:"operation,omitempty"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// IndexRestorer is the narrow application-layer port for the restore hop: it
// re-emits the canonical asset.index.requested envelope for the asset so the
// index plane rebuilds the projection from scratch.
//
// Production concrete: *pgmedia.PostgresMediaCommitter, whose
// RestoreIndexedAsset opens ONE PostgreSQL transaction that reads the asset's
// index identity and commits the canonical index request against the media
// SSOT.
type IndexRestorer interface {
	RestoreIndexedAsset(ctx context.Context, assetID string) error
}

// IndexRestoreHandler is the real handler for
// asset.index.restore_requested.v1.
type IndexRestoreHandler struct {
	log      *zap.Logger
	restorer IndexRestorer
}

// NewIndexRestoreHandler wires the handler's narrow port. log nil → nop logger.
// A nil restorer fails closed on every event rather than silently acking.
func NewIndexRestoreHandler(log *zap.Logger, restorer IndexRestorer) *IndexRestoreHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &IndexRestoreHandler{log: log.Named("index_restore"), restorer: restorer}
}

// EventType returns the canonical outboxevents constant.
func (h *IndexRestoreHandler) EventType() string {
	return IndexRestoreEventType
}

// IdempotencyKey declares the canonical handler-level identity (static, derived
// from the schema version) so HandlerRegistry.Register's fail-closed panic
// fires at init time if a refactor drops it.
func (h *IndexRestoreHandler) IdempotencyKey() string {
	return IndexRestoreEventType + "." + IndexRestoreRequestSchemaVersion
}

// Handle validates the v1 envelope and re-emits the canonical index request.
//
// Envelope problems are TERMINAL (retrying cannot conjure a missing field);
// a re-emit failure is retryable, because a transient media-SSOT error is
// exactly the case the outbox retry budget exists for.
func (h *IndexRestoreHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}

	var req indexRestoreRequestV1
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &req); err != nil {
		log.Warn("asset.index.restore_requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID), zap.String("aggregate_id", evt.AggregateID), zap.Error(err))
		return outboxevents.NewTerminalError(fmt.Errorf("%w: payload parse: %v", indexRestoreTerminalErr, err))
	}
	if req.SchemaVersion != IndexRestoreRequestSchemaVersion {
		log.Warn("asset.index.restore_requested schema_version mismatch (terminal)",
			zap.Int64("event_id", evt.ID), zap.String("got_version", req.SchemaVersion),
			zap.String("want_version", IndexRestoreRequestSchemaVersion))
		return outboxevents.NewTerminalError(fmt.Errorf(
			"%w: schema_version %q != %q", indexRestoreTerminalErr, req.SchemaVersion, IndexRestoreRequestSchemaVersion))
	}
	// The asset identity prefers the envelope field and falls back to the
	// aggregate id, exactly like the sibling index/delete handlers.
	assetID := req.AssetID
	if assetID == "" {
		assetID = evt.AggregateID
	}
	if assetID == "" {
		log.Warn("asset.index.restore_requested: empty asset_id (terminal)", zap.Int64("event_id", evt.ID))
		return outboxevents.NewTerminalError(fmt.Errorf("%w: asset_id is required", indexRestoreTerminalErr))
	}
	if req.IdempotencyKey == "" {
		log.Warn("asset.index.restore_requested: missing idempotency_key (terminal)",
			zap.Int64("event_id", evt.ID), zap.String("asset_id", assetID))
		return outboxevents.NewTerminalError(fmt.Errorf("%w: idempotency_key is required", indexRestoreTerminalErr))
	}

	if h.restorer == nil {
		return outboxevents.NewTerminalError(fmt.Errorf("%w: restore port is not wired", indexRestoreTerminalErr))
	}
	if err := h.restorer.RestoreIndexedAsset(ctx, assetID); err != nil {
		log.Warn("asset.index.restore_requested: re-emit index request failed (retryable)",
			zap.String("asset_id", assetID), zap.Int("attempt", evt.AttemptCount), zap.Error(err))
		return fmt.Errorf("asset.index.restore_requested re-emit(%s): %w", assetID, err)
	}

	log.Info("asset.index.restore_requested: restore dispatched",
		zap.String("asset_id", assetID),
		zap.Int64("event_id", evt.ID),
		zap.String("idempotency_key", req.IdempotencyKey),
	)
	return nil
}

var _ outboxevents.Handler = (*IndexRestoreHandler)(nil)

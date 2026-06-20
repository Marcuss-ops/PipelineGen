package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// IndexClipper is the minimum surface the IndexingHandler needs from the
// clipindexer.Service. Defined as an interface so the handler does not
// import clipindexer directly (avoids a circular dependency when the
// clipindexer package itself produces outbox events).
type IndexClipper interface {
	IndexClip(ctx context.Context, clipID string) error
}

// IndexingHandler processes "asset.index.requested" events by running the
// full clipindexer pipeline (embedding generation + Qdrant upsert). This
// replaces the legacy media_index_outbox Worker that polled its own table
// and called IndexClip on each claim.
//
// The handler is concurrency-safe — the outboxevents Pool runs
// Handle from N worker goroutines.
type IndexingHandler struct {
	indexer IndexClipper
	log     *zap.Logger
}

// NewIndexingHandler creates an IndexingHandler. indexer may be nil for
// tests (Handle will return an error on any non-trivial call). log may be
// nil — a nop logger is substituted.
func NewIndexingHandler(indexer IndexClipper, log *zap.Logger) *IndexingHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &IndexingHandler{indexer: indexer, log: log}
}

// EventType returns "asset.index.requested".
func (h *IndexingHandler) EventType() string {
	return outboxevents.EventAssetIndexRequested
}

// indexRequestPayload is the JSON schema for asset.index.requested events.
type indexRequestPayload struct {
	AssetID string `json:"asset_id"`
}

// Handle parses the payload to extract the asset_id and delegates to
// indexer.IndexClip. Returns an error on malformed payload or indexing
// failure — the outboxevents Pool will call MarkFailed (retry or
// dead_letter) on non-nil returns.
func (h *IndexingHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	// nil-safe: use nop logger so tests can construct without a logger.
	log := h.log
	if log == nil {
		log = zap.NewNop()
	}
	var p indexRequestPayload
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &p); err != nil {
		log.Warn("asset.index.requested payload parse failed",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return fmt.Errorf("asset.index.requested payload parse: %w", err)
	}
	if p.AssetID == "" {
		return fmt.Errorf("asset.index.requested: empty asset_id in payload")
	}

	log.Info("indexing asset via outbox_events handler",
		zap.String("asset_id", p.AssetID),
		zap.Int64("event_id", evt.ID),
		zap.Int("attempt", evt.AttemptCount),
	)

	if err := h.indexer.IndexClip(ctx, p.AssetID); err != nil {
		log.Warn("asset.index.requested: IndexClip failed",
			zap.String("asset_id", p.AssetID),
			zap.Int64("event_id", evt.ID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(err),
		)
		return fmt.Errorf("asset.index.requested IndexClip(%s): %w", p.AssetID, err)
	}

	log.Info("asset indexed via outbox_events handler",
		zap.String("asset_id", p.AssetID),
		zap.Int64("event_id", evt.ID),
	)
	return nil
}

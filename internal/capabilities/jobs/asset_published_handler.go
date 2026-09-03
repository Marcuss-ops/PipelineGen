// Package jobs contains the informational asset.published consumer.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ErrAssetPublishedTerminalEnvelope aggregates every payload-validation
// failure. Retry cannot repair a missing or malformed required field.
var ErrAssetPublishedTerminalEnvelope = errors.New("asset.published: terminal envelope error")

// ErrAssetPublishedPayloadParse fires when the JSON body is malformed.
var ErrAssetPublishedPayloadParse = fmt.Errorf("%w: payload parse failed", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedSchemaVersionMismatch fires when schema_version does not
// match AssetPublishedSchemaVersion.
var ErrAssetPublishedSchemaVersionMismatch = fmt.Errorf("%w: schema version mismatch", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedAssetIDMissing fires when asset_id is empty.
var ErrAssetPublishedAssetIDMissing = fmt.Errorf("%w: asset_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedDestinationMissing fires when destination is empty.
var ErrAssetPublishedDestinationMissing = fmt.Errorf("%w: destination is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedEventIDMissing fires when event_id is empty.
var ErrAssetPublishedEventIDMissing = fmt.Errorf("%w: event_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedIdempotencyKeyMissing fires when idempotency_key is empty.
var ErrAssetPublishedIdempotencyKeyMissing = fmt.Errorf("%w: idempotency_key is required", ErrAssetPublishedTerminalEnvelope)

type AssetPublishedHandler struct {
	log *zap.Logger
}

// NewAssetPublishedHandler constructs the informational consumer. Valid
// asset.published events never invoke Qdrant or mutate index state;
// asset.index.requested.v1 is the sole operational indexing command.
func NewAssetPublishedHandler(log *zap.Logger) *AssetPublishedHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetPublishedHandler{log: log.Named("asset_published")}
}

func (h *AssetPublishedHandler) EventType() string {
	return outboxevents.EventAssetPublished
}

func (h *AssetPublishedHandler) IdempotencyKey() string {
	return outboxevents.EventAssetPublished + "." + outboxevents.SchemaVersionAssetPublished
}

// ComposeSearchText remains the canonical informational representation used
// for audit/debug output. It does not perform persistence or indexing.
func ComposeSearchText(destination, origin, subject, category, provider string, tags []string, drivePath, contentType string) string {
	if destination == "" {
		return ""
	}
	parts := []string{destination}
	if origin != "" {
		parts = append(parts, origin)
	}
	parts = append(parts, "about")
	if subject != "" {
		parts = append(parts, subject)
	}
	if category != "" {
		parts = append(parts, "in", "category", category)
	}
	if provider != "" {
		parts = append(parts, "from", "provider", provider)
	}
	if len(tags) > 0 {
		kept := make([]string, 0, len(tags))
		for _, tag := range tags {
			if tag = strings.TrimSpace(tag); tag != "" {
				kept = append(kept, tag)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, "tags", strings.Join(kept, " "))
		}
	}
	if drivePath != "" {
		parts = append(parts, "in", "drive", drivePath)
	}
	if contentType != "" {
		parts = append(parts, "content_type", contentType)
	}
	return strings.Join(parts, " ")
}

// Handle validates asset.published.v1 and records receipt. It deliberately
// performs no Qdrant write and no media_assets.index_state transition.
func (h *AssetPublishedHandler) Handle(ctx context.Context, evt outboxevents.Event) error {
	_ = ctx
	start := time.Now()
	outcome := "parse_err"
	defer func() {
		h.log.Debug("asset.published: informational outcome",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("outcome", outcome),
			zap.Duration("duration", time.Since(start)),
		)
	}()

	var payload AssetPublishedRequestV1
	if err := json.Unmarshal([]byte(evt.PayloadJSON), &payload); err != nil {
		h.log.Warn("asset.published payload parse failed (terminal)", zap.Error(err))
		return outboxevents.NewTerminalError(fmt.Errorf("%w: %v", ErrAssetPublishedPayloadParse, err))
	}
	if payload.SchemaVersion != AssetPublishedSchemaVersion {
		return outboxevents.NewTerminalError(fmt.Errorf("%w: got %q, want %q", ErrAssetPublishedSchemaVersionMismatch, payload.SchemaVersion, AssetPublishedSchemaVersion))
	}
	if payload.EventID == "" {
		return outboxevents.NewTerminalError(ErrAssetPublishedEventIDMissing)
	}
	if payload.AssetID == "" {
		return outboxevents.NewTerminalError(ErrAssetPublishedAssetIDMissing)
	}
	if payload.Destination == "" {
		return outboxevents.NewTerminalError(ErrAssetPublishedDestinationMissing)
	}
	if payload.IdempotencyKey == "" {
		return outboxevents.NewTerminalError(ErrAssetPublishedIdempotencyKeyMissing)
	}

	searchText := ComposeSearchText(payload.Destination, payload.Origin, payload.Subject, payload.Category, payload.Provider, payload.Tags, payload.DrivePath, payload.ContentType)
	outcome = "informational"
	h.log.Info("asset.published: informational event received",
		zap.String("asset_id", payload.AssetID),
		zap.String("event_id", payload.EventID),
		zap.String("idempotency_key", payload.IdempotencyKey),
		zap.String("search_text", searchText),
	)
	return nil
}

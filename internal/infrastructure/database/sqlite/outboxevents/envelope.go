// envelope.go — canonical outbox envelope builders.
//
// Pulled out of cmd/admin/reconcile_qdrant.go (QDRANT-005B PR1 +
// QDRANT-005C hygiene commit) so the admin reconcile adapter becomes
// a 1-line caller. Application-side writers (outbox.Dispatcher) emit
// the same schema_version literal; this file is the cmd-side mirror
// that the admin path uses when bypassing the application dispatcher
// to avoid pulling in production assets.ClipsRepository (which would
// create an import cycle at the admin binary).
//
// Pattern:
//
//   ok := outboxevents.NewRepository(db)
//   eventKey, payload, err := outboxevents.BuildReindexEnvelopeV1(assetID, schemaVersion, time.Now())
//   if err != nil { ... }
//   ok.Enqueue(ctx, tx, outboxevents.EventAssetIndexRequested, assetID, "media_asset", payload, eventKey)
//
// Idempotency policy: every builder below suffixes the event key
// with a fresh UUID so ON CONFLICT (event_key) DO NOTHING never
// suppresses a fresh reconcile-repair. The worker downstream honors
// supersede semantically — see outbox.MarkSuperseded for the path.
package outboxevents

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ReindexEnvelopeV1Schema is the canonical schema_version literal
// for asset.index.requested.v1 events. Mirrored from outbox.Dispatcher
// (see application side for the production writers).
const ReindexEnvelopeV1Schema = "asset.index.requested.v1"

// BuildReindexEnvelopeV1 returns (eventKey, payloadJSON) for an
// asset.index.requested.v1 outbox event.
//
// Idempotency: eventKey is shaped "reconcile:reindex:<assetID>:<uuid>"
// with a FRESH UUID per call. ON CONFLICT (event_key) DO NOTHING
// therefore never swallows a new reconcile-repair; re-running
// reconcile-qdrant --apply twice produces two distinct events and the
// worker downstream collapses redundancy using the supersede gate
// (outbox.MarkSuperseded).
//
// Schema-version mirror: payload shape matches production
// outbox.Dispatcher.EnqueueAndIndex (see application outbox package).
//
// Returns an error if assetID is empty or marshalling fails; the
// caller is responsible for wrapping the error with the local context.
func BuildReindexEnvelopeV1(assetID, targetSchemaVersion string, requestedAt time.Time) (eventKey, payloadJSON string, err error) {
	if assetID == "" {
		return "", "", fmt.Errorf("BuildReindexEnvelopeV1: assetID must not be empty")
	}
	eventID := uuid.NewString()
	eventKey = "reconcile:reindex:" + assetID + ":" + eventID
	payload := map[string]any{
		"schema_version":       ReindexEnvelopeV1Schema,
		"event_id":             eventID,
		"asset_id":             assetID,
		"operation":            "UPSERT",
		"source_version":       eventID,
		"target_index_version": targetSchemaVersion,
		"requested_vectors":    []string{"text", "transcript"},
		"requested_at":         requestedAt.UTC().Format(time.RFC3339Nano),
		"idempotency_key":      eventKey,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("BuildReindexEnvelopeV1: marshal: %w", err)
	}
	payloadJSON = string(payloadBytes)
	return eventKey, payloadJSON, nil
}

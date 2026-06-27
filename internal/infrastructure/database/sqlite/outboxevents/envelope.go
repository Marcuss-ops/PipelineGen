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
//	ok := outboxevents.NewRepository(db)
//	eventKey, payload, err := outboxevents.BuildReindexEnvelopeV1(assetID, schemaVersion, contentHash, time.Now())
//	if err != nil { ... }
//	ok.Enqueue(ctx, tx, outboxevents.EventAssetIndexRequested, assetID, "media_asset", payload, eventKey)
//
// Idempotency policy (PR 11 hardening, June 2026):
//
//	eventKey is shaped "reconcile:reindex:<assetID>:<targetSchemaVersion>:<full_content_hash>"
//
// The hash is the FULL ingest-time content_hash of the asset row (read
// inside the producer tx for atomic capture), NOT a per-call UUID.
// ON CONFLICT (event_key) DO NOTHING therefore collapses repeated
// reconcile-repair --apply runs on the same (asset, schema, hash)
// tuple into a single outbox_events row — the second apply is a
// no-op at the SQLite level. The supersede gate downstream (worker
// source_version comparison) collapses any out-of-order racing
// repair with a fresh content_hash when the underlying asset has
// actually changed.
//
// SourceVersion contract:
//
//   - Must be a deterministic per-asset fingerprint (content_hash,
//     file_hash, or equivalent).
//   - Two reconcile-repair runs on the same (assetID, schemaVersion,
//     sourceVersion) tuple MUST produce the same event_key.
//   - Two reconcile-repair runs where sourceVersion differs MUST
//     produce distinct event_keys so the worker downstream can
//     re-evaluate against the new fingerprint.
//
// Fail-closed posture: empty targetSchemaVersion or empty sourceVersion
// return an error rather than producing a deterministic-but-misleading
// key (the dispatcher would enqueue an un-routable event).
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
// eventKey shape: "reconcile:reindex:<assetID>:<targetSchemaVersion>:<sourceVersion>"
//
// Idempotency invariants (PR 11, June 2026):
//
//   - Identical (assetID, targetSchemaVersion, sourceVersion) over two
//     calls produces the same eventKey. ON CONFLICT (event_key) DO
//     NOTHING in Repository.Enqueue collapses the second apply into a
//     no-op at the SQLite level — only one outbox_events row exists.
//   - A sourceVersion change (asset's content_hash mutated between
//     reconcile runs) produces a different eventKey, so the new row
//     is enqueued and the worker can re-evaluate.
//   - A targetSchemaVersion change (Qdrant collection / schema
//     upgrade between reconcile runs) produces a different eventKey,
//     so the new row is enqueued and the worker can re-evaluate.
//
// Schema-version mirror: payload shape matches production
// outbox.Dispatcher.EnqueueAndIndex (see application outbox package).
//
// eventID is a per-call UUID stamped into the payload JSON for
// operator audit (visible in logs and dashboards) and is NOT used in
// the event_key — separating the audit token from the idempotency
// token means a re-emitted event with the same key still has a
// distinguishable event_id.
//
// Returns an error if any required field is empty or marshalling
// fails; the caller is responsible for wrapping the error with
// the local context.
func BuildReindexEnvelopeV1(assetID, targetSchemaVersion, sourceVersion string, requestedAt time.Time) (eventKey, payloadJSON string, err error) {
	if assetID == "" {
		return "", "", fmt.Errorf("BuildReindexEnvelopeV1: assetID must not be empty")
	}
	if targetSchemaVersion == "" {
		return "", "", fmt.Errorf("BuildReindexEnvelopeV1: targetSchemaVersion must not be empty")
	}
	if sourceVersion == "" {
		// Empty sourceVersion is fail-closed: it would produce a
		// deterministic-but-misleading event_key that masks real
		// content hash changes (every empty-sourceVersion event
		// for the same assetID would collapse into one row).
		// Producers MUST supply an ingest-time fingerprint.
		return "", "", fmt.Errorf("BuildReindexEnvelopeV1: sourceVersion must not be empty (PR 11 — deterministic event_key requires the ingest-time content_hash)")
	}
	eventID := uuid.NewString()
	eventKey = "reconcile:reindex:" + assetID + ":" + targetSchemaVersion + ":" + sourceVersion
	payload := map[string]any{
		"schema_version":       ReindexEnvelopeV1Schema,
		"event_id":             eventID,
		"asset_id":             assetID,
		"operation":            "UPSERT",
		"source_version":       sourceVersion,
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

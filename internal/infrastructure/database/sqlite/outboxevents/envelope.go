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
// Card 7.1 (July 2026): two public variants now exist —
// BuildReindexEnvelopeV1 (the canonical 4-arg form, force=false) and
// BuildReindexEnvelopeV1Force (the new force=true variant for admin
// reindex). Both delegate to the private buildReindexEnvelopeV1
// helper which is the single source of envelope-shape truth. The
// 56+ existing callers of BuildReindexEnvelopeV1 are unchanged
// (force=false semantics preserved).
//
// Pattern:
//
//	ok := outboxevents.NewRepository(db)
//	eventKey, payload, err := outboxevents.BuildReindexEnvelopeV1(assetID, schemaVersion, contentHash, time.Now())
//	if err != nil { ... }
//	ok.Enqueue(ctx, tx, outboxevents.EventAssetIndexRequested, assetID, "media_asset", payload, eventKey)
//
// Idempotency policy (PR 11 hardening, June 2026; Card 7.1 extension):
//
//	eventKey is shaped "reconcile:reindex:<assetID>:<targetSchemaVersion>:<full_content_hash>"
//
//	When force=true (BuildReindexEnvelopeV1Force) the eventKey
//	appends the literal ":force" suffix, so a forced reindex
//	survives a prior non-forced reindex for the same (assetID,
//	schemaVersion, sourceVersion) tuple — without the suffix
//	the SQLite UNIQUE(event_key) constraint would collapse them
//	via ON CONFLICT DO NOTHING and the operator's force would
//	be silently swallowed.
//
// The hash is the FULL ingest-time content_hash of the asset row
// (read inside the producer tx for atomic capture), NOT a per-call
// UUID. ON CONFLICT (event_key) DO NOTHING therefore collapses
// repeated reconcile-repair --apply runs on the same (asset, schema,
// hash) tuple into a single outbox_events row — the second apply is
// a no-op at the SQLite level. The supersede gate downstream
// (worker source_version comparison) collapses any out-of-order
// racing repair with a fresh content_hash when the underlying asset
// has actually changed. Card 7.1's force=true bypasses the supersede
// gate (the operator explicitly opts in to force-reindex regardless
// of the current content_hash).
//
// SourceVersion contract:
//
//   - Must be a deterministic per-asset fingerprint (content_hash,
//     file_hash, or equivalent).
//   - Two reconcile-repair runs on the same (assetID, schemaVersion,
//     sourceVersion) tuple MUST produce the same eventKey.
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

// forceEventKeySuffix is appended to the event_key ONLY when
// BuildReindexEnvelopeV1Force is called (force=true). Lets a forced
// reindex survive a prior non-forced reindex for the same
// (assetID, schemaVersion, sourceVersion) tuple.
//
// Card 7.1 (July 2026): see package doc for the full rationale.
const forceEventKeySuffix = ":force"

// BuildReindexEnvelopeV1 returns (eventKey, payloadJSON) for an
// asset.index.requested.v1 outbox event with force=false (the
// production default: the worker supersede gate applies, so an
// event whose source_version differs from the asset's current
// fingerprint is marked SUPERSEDED without burning a Qdrant upsert).
//
// Card 7.1 (July 2026): the 4-arg shape is preserved for backward
// compat across the 56+ existing callers. The new
// BuildReindexEnvelopeV1Force variant is the explicit opt-in for
// admin reindex; both variants route through the private
// buildReindexEnvelopeV1 helper.
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
	return buildReindexEnvelopeV1(assetID, targetSchemaVersion, sourceVersion, requestedAt, false)
}

// BuildReindexEnvelopeV1Force is the admin reindex variant of
// BuildReindexEnvelopeV1 (Card 7.1, July 2026). The force=true
// semantic propagates through the envelope payload (the JSON
// `force: true` field) and through the event_key (the literal
// ":force" suffix) so the SQLite UNIQUE(event_key) dedup does not
// collapse a forced reindex with a prior non-forced reindex for
// the same (assetID, schemaVersion, sourceVersion) tuple.
//
// The worker (IndexingHandler.Handle) reads payload.force and
// uses the source_version supersede exception when force=true,
// re-running IndexClip unconditionally. This is the canonical
// admin reindex path: the operator explicitly opts in to "reindex
// regardless of current fingerprint" semantics, and the outbox
// preserves that intent through the worker contract.
//
// Production ingest (asset_committer.go::SQLiteAssetCommitter) and
// the normal reconciler repair path (service_projection.go) must
// NOT use this variant — both honor the supersede dedup gate by
// design.
func BuildReindexEnvelopeV1Force(assetID, targetSchemaVersion, sourceVersion string, requestedAt time.Time) (eventKey, payloadJSON string, err error) {
	return buildReindexEnvelopeV1(assetID, targetSchemaVersion, sourceVersion, requestedAt, true)
}

// buildReindexEnvelopeV1 is the canonical envelope builder. The two
// public variants (BuildReindexEnvelopeV1 + BuildReindexEnvelopeV1Force)
// are thin wrappers that pin force=false/true respectively.
//
// Event_key shape:
//   - force=false: "reconcile:reindex:<assetID>:<schema>:<source>"
//   - force=true:  "reconcile:reindex:<assetID>:<schema>:<source>:force"
//
// Payload fields (Card 7.1 adds the `force` field):
//   - schema_version, event_id, asset_id, operation, source_version,
//     target_index_version, requested_vectors, requested_at,
//     idempotency_key (unchanged) + force (NEW, always present).
//
// Idempotency invariants: see package doc.
func buildReindexEnvelopeV1(assetID, targetSchemaVersion, sourceVersion string, requestedAt time.Time, force bool) (eventKey, payloadJSON string, err error) {
	if assetID == "" {
		return "", "", fmt.Errorf("buildReindexEnvelopeV1: assetID must not be empty")
	}
	if targetSchemaVersion == "" {
		return "", "", fmt.Errorf("buildReindexEnvelopeV1: targetSchemaVersion must not be empty")
	}
	if sourceVersion == "" {
		// Empty sourceVersion is fail-closed: it would produce a
		// deterministic-but-misleading event_key that masks real
		// content hash changes (every empty-sourceVersion event
		// for the same assetID would collapse into one row).
		// Producers MUST supply an ingest-time fingerprint.
		return "", "", fmt.Errorf("buildReindexEnvelopeV1: sourceVersion must not be empty (PR 11 — deterministic event_key requires the ingest-time content_hash)")
	}
	eventID := uuid.NewString()
	eventKey = "reconcile:reindex:" + assetID + ":" + targetSchemaVersion + ":" + sourceVersion
	if force {
		eventKey += forceEventKeySuffix
	}
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
		"force":                force,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("buildReindexEnvelopeV1: marshal: %w", err)
	}
	payloadJSON = string(payloadBytes)
	return eventKey, payloadJSON, nil
}

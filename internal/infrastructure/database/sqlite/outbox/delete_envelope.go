package outbox

import (
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
)

// deleteRequestV1 is the producer-side mirror of the consumer
// (application/jobs/outbox.index_delete.go::indexDeleteRequestV1).
//
// QDRANT-002 PR7: this envelope is emitted by Dispatcher.EnqueueAndDelete
// inside an atomic tx together with the media_assets.index_state
// transition to DELETE_PENDING. The schema_version literal MUST match
// DeleteRequestSchemaVersion on the consumer side — mismatch is
// classified as TERMINAL by the consumer (QDRANT-002 PR4 invariant I).
//
// Producers MUST send the asset_id and a stable event_id; idempotency_key
// is the dedup token shared with the outbox_events.event_key column (the
// v1 conflation invariant is identical to the index envelope, see
// repository.go::EnqueueAndIndex for the full rationale). The
// source_version field is intentionally ABSENT — delete events are not
// subject to a supersede gate (a re-insert wins by emitting a fresh
// asset.index.requested event into the same outbox queue, not by
// invalidating the pending delete).
type deleteRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// buildDeleteRequestV1 constructs the canonical producer-side
// envelope for an asset.index.delete_requested.v1 event. id is the
// canonical media_assets.id (NOT a content fingerprint — see
// deleteEventKey for the key-shape rationale).
func buildDeleteRequestV1(id string) deleteRequestV1 {
	eventID := uuid.NewString()
	return deleteRequestV1{
		SchemaVersion: DeleteRequestSchemaVersion,
		EventID:       eventID,
		AssetID:       id,
		RequestedAt:   timeutil.FormatRFC3339(time.Now()),
		// Idempotency-Key vs event-key conflation invariant (mirrors
		// the index envelope in repository.go::EnqueueAndIndex):
		// payload.IdempotencyKey MUST equal outbox_events.event_key at
		// insert time. Dispatcher.EnqueueAndDelete enforces this with
		// a runtime assertion — any future split lands as a v2
		// envelope change, not a silent drift.
		IdempotencyKey: deleteEventKey(id),
	}
}

// deleteEventKey computes the canonical event_key for a delete event.
// QDRANT-002 PR7 (thinker recommendation): shape is `delete:<asset_id>.
// A repeated POST for the same aggregate id collapses to a single
// outbox row (ON CONFLICT(event_key) DO NOTHING), so an operator
// rapid-firing 5 deletes sees exactly one queued event per asset.
// Content fingerprint and collection_version are NOT included because
// a delete invalidates "everything for this asset" rather than a
// specific content version — see repository.go::EnqueueAndIndex's
// event_key for the symmetric index-side rationale.
func deleteEventKey(assetID string) string {
	return "delete:" + assetID
}

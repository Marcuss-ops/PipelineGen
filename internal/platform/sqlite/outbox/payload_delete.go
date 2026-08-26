package outbox

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Delete/Restore Payloads ────────────────────────────────────────────

// restoreRequestV1 is the canonical envelope Dispatcher emits on
// the asset.index.restore_requested event type (Wave 22, task 1
// of 5 foundation; handler lands in task 3 of 5). Schema mirrors
// indexRequestV1 + deleteRequestV1 from sibling method blocks so
// the consumer-side decoder (future RestoreHandler) can re-use
// the v1 conflation invariant + event_key canonicalisation.
type restoreRequestV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	Operation      string `json:"operation"`
	IdempotencyKey string `json:"idempotency_key"`
	RequestedAt    string `json:"requested_at"`
}

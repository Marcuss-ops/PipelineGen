// Package fixture — self-check fixture for Check 11 (TODO 16).
//
// This file (check_11_uuid_event_key.go) demonstrates the
// event_key construction with random UUID anti-pattern. The canonical
// event_key shape is deterministic (delete:<asset_id> or
// reindex:<asset_id>:<hash>); uuid.NewString in the key shape forces
// every enqueue to produce a new outbox row, defeating
// ON CONFLICT(event_key) DO NOTHING.
package fixture

import "github.com/google/uuid"

// Forbidden: `eventKey` constructed with a random UUID suffix.
// The canonical pattern is `eventKey := "delete:" + assetID` (see
// internal/infrastructure/database/sqlite/outbox/delete_envelope.go)
// or the index envelope in outboxevents/repository.go.
//
// Single-line shape so the regex `eventKey.*uuid\.NewString` matches
// the actual code. The production code at cmd/admin/reconcile_qdrant.go
// uses a multi-line variant (`eventID := uuid.NewString(); eventKey :=
// "..." + eventID`) that the current single-line check does NOT catch;
// that gap is tracked as TODO 16 follow-up and will be closed with a
// multiline rg -U pattern in a separate PR.
func badUuidEventKey(assetID string) string {
	eventKey := "reconcile:delete:" + assetID + ":" + uuid.NewString() // anti-pattern: random UUID in key
	return eventKey
}

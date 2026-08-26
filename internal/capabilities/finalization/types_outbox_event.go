// types/types_outbox_event.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

import (
)

import "encoding/json"

// OutboxEvent is a domain-level descriptor for an event that must be
// committed atomically alongside the job completion. The concrete
// payload and event type are capability-specific.
type OutboxEvent struct {
	// EventType is the canonical event type string (e.g.
	// "asset.index_requested.v1").
	EventType string `json:"event_type"`

	// AggregateID identifies the domain aggregate this event belongs to
	// (typically the asset ID or job ID).
	AggregateID string `json:"aggregate_id"`

	// EventKey is a deterministic deduplication key.
	EventKey string `json:"event_key"`

	// Payload is the event-specific JSON payload.
	Payload json.RawMessage `json:"payload"`
}

// Package outbox defines the canonical domain types for the transactional
// outbox pattern. Every code path that mutates authoritative data AND triggers
// an external side-effect (Drive upload, webhook, notification, external
// indexing) MUST route through this package's Repository interface.
//
// Pattern:
//
//	BEGIN
//	UPDATE media_assets ...
//	INSERT INTO outbox_events (...) VALUES (...) ON CONFLICT(event_key) DO NOTHING
//	COMMIT
//
// A worker polls ClaimNext and dispatches to the appropriate handler.
// Events have four states: pending, processing, completed, dead_letter.
package outbox

import (
	"errors"
	"time"
)

// Canonical event type constants. Every outbox event MUST use one of these
// as its event_type column value.
const (
	EventAssetIndexRequested          = "asset.index.requested"
	EventDeliveryRequested            = "delivery.requested"
	EventAssetMetadataExportRequested = "asset.metadata_export.requested"
	EventProviderSyncRequested        = "provider.sync.requested"
	EventWorkflowStepCompleted        = "workflow.step.completed"
	EventWorkflowStepFailed           = "workflow.step.failed"
)

// EventStatus is the 4-state lifecycle of an outbox event.
type EventStatus string

const (
	StatusPending    EventStatus = "pending"
	StatusProcessing EventStatus = "processing"
	StatusCompleted  EventStatus = "completed"
	StatusDeadLetter EventStatus = "dead_letter"
)

// Event represents a single outbox_events row.
type Event struct {
	ID            int64      `json:"id"`
	EventType     string     `json:"event_type"`
	AggregateID   string     `json:"aggregate_id"`
	AggregateType string     `json:"aggregate_type"`
	PayloadJSON   string     `json:"payload_json"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	MaxAttempts   int        `json:"max_attempts"`
	LastError     string     `json:"last_error"`
	EventKey      string     `json:"event_key"`
	WorkerID      string     `json:"worker_id"`
	LeaseID       string     `json:"lease_id"`
	LeaseExpiry   *time.Time `json:"lease_expiry,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ErrLeaseLost is returned by MarkCompleted, MarkFailed, and RenewLease
// when the caller's lease_id no longer matches the event's current lease —
// meaning the event was reassigned after lease expiry or has already
// reached a terminal status.
var ErrLeaseLost = errors.New("outbox lease lost")

// Claim is the fencing token returned by ClaimNext. It bundles the claimed
// event with the worker and lease IDs required for subsequent operations
// (MarkCompleted, MarkFailed, RenewLease).
type Claim struct {
	Event    Event  `json:"event"`
	WorkerID string `json:"worker_id"`
	LeaseID  string `json:"lease_id"`
}

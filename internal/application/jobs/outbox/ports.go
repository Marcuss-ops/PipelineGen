// Package outbox — typed application-layer port for the read-only outbox
// events monitoring HTTP handler (QDRANT-002).
//
// Wave 14 (June 2026): this port replaces the previous direct dependency
// from internal/api/outbox/handler.go on the concrete
// *outboxevents.Repository. Per AGENTS.md Pattern 0 + Pattern 8 ("API
// package: thin transport only"), the handler now depends on this
// interface; the concrete adapter that wraps *outboxevents.Repository
// lives in internal/app/outbox_monitor_adapter.go with a compile-time
// `var _ outbox.MonitorPort = (*outboxMonitorAdapter)(nil)` assertion.
//
// The EventDTO mirrors the JSON-relevant subset of outboxevents.Event.
// Field names match the unexported-tag default JSON encoding
// (PascalCase keys) so wire-format compatibility is preserved bit-for-bit
// when the handler is migrated from the concrete repo to the port.
package outbox

import (
	"context"
	"time"
)

// MonitorPort is the canonical narrow surface for the outbox events
// monitoring HTTP endpoint. The two methods exposed are exactly the
// ones exercised by internal/api/outbox/handler.go (handleStatus +
// handleEvents). Other *outboxevents.Repository methods (Enqueue,
// ClaimNext, MarkCompleted, MarkFailed, MarkDeadLetter, MarkSuperseded,
// RequeueExpiredLeases, RenewLease) are intentionally NOT on the port
// — the API is read-only by design.
type MonitorPort interface {
	// CountByStatus returns the count of outbox events in a given
	// status bucket (e.g. "pending", "processing", "completed",
	// "dead_letter", "superseded").
	CountByStatus(ctx context.Context, status string) (int64, error)
	// ListPending returns the events currently in pending or
	// processing state, ordered by created_at ASC. Used by the
	// operator dashboard feed.
	ListPending(ctx context.Context) ([]EventDTO, error)
}

// EventDTO mirrors the JSON-relevant subset of outboxevents.Event.
// Field names preserve the same JSON keys the handler emitted when it
// directly returned []outboxevents.Event to gin.H (Go's default
// JSON encoding uses the exported field names verbatim). Time fields
// keep their original string-RFC3339 representation because the
// underlying outbox_events schema stores them as TEXT, not TIMESTAMP.
// Pointer-to-time fields (LeaseExpiry, CompletedAt) are kept as
// pointer-to-time so the JSON output preserves the null-vs-string
// distinction the operator dashboard relies on.
type EventDTO struct {
	ID            int64      `json:"ID"`
	EventType     string     `json:"EventType"`
	AggregateID   string     `json:"AggregateID"`
	AggregateType string     `json:"AggregateType"`
	PayloadJSON   string     `json:"PayloadJSON"`
	Status        string     `json:"Status"`
	AttemptCount  int        `json:"AttemptCount"`
	MaxAttempts   int        `json:"MaxAttempts"`
	LastError     string     `json:"LastError"`
	EventKey      string     `json:"EventKey"`
	WorkerID      string     `json:"WorkerID"`
	LeaseID       string     `json:"LeaseID"`
	LeaseExpiry   *time.Time `json:"LeaseExpiry"`
	CompletedAt   *time.Time `json:"CompletedAt"`
	CreatedAt     string     `json:"CreatedAt"`
	UpdatedAt     string     `json:"UpdatedAt"`
}

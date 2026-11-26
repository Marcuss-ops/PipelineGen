// Package outbox owns the ENGINE-INDEPENDENT outbox contract: the row DTO a
// handler receives, the handler interface and registry, the v1 envelope decode
// rule and the enqueue feedback DTO.
//
// Why this package exists: every capability that consumes or produces outbox
// events used to import internal/platform/sqlite/outboxevents for these types.
// That made a transport/storage adapter a compile-time dependency of the
// business layer, so a change to the SQLite adapter (or a move to another
// engine) crossed the core of the capability. The SQL adapter still owns the
// SQL (repository, pool, supersede routing); it now imports this contract
// instead of declaring it.
//
// The event-name and schema-version literals are NOT declared here either:
// they are owned by internal/kernel/event and re-exported as compile-time
// aliases, so the SQLite adapter, the PostgreSQL adapter and this package can
// never drift (percheck_identity_ssot).
package outbox

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Event represents a single outbox event row as a handler sees it.
type Event struct {
	ID            int64
	EventType     string
	AggregateID   string
	AggregateType string
	PayloadJSON   string
	Status        string
	AttemptCount  int
	MaxAttempts   int
	LastError     string
	EventKey      string
	WorkerID      string
	LeaseID       string
	LeaseExpiry   *time.Time
	CompletedAt   *time.Time
	CreatedAt     string
	UpdatedAt     string
	// Priority is the scheduling priority stamped by the producer
	// (migration 186). ClaimNext claims higher values first; default 5.
	Priority int
}

// EnqueueResult is the typed feedback from enqueueing an event. Before Blocco
// 2.0, ON CONFLICT(event_key) DO NOTHING silently suppressed duplicate inserts
// with zero feedback — the producer had no way to know whether the event was
// freshly inserted or silently ignored (potentially because a
// completed/dead_letter/superseded row already existed with the same
// event_key).
//
// Inserted=true means the INSERT landed (no conflict existed). Inserted=false
// means ON CONFLICT fired; ExistingStatus carries the existing row's status so
// the producer can decide whether to retry with a new event_key, surface a
// warning, or move on.
type EnqueueResult struct {
	EventID               int64  // ID of the row (new or existing)
	Inserted              bool   // true if INSERT landed, false if ON CONFLICT suppressed
	ExistingStatus        string // existing row's status when Inserted=false; empty when Inserted=true
	ExistingEventType     string // existing event type when Inserted=false
	ExistingAggregateType string // existing aggregate type when Inserted=false
	ExistingAggregateID   string // existing aggregate ID when Inserted=false
}

// Handler consumes one event type. Implementations MUST be safe for concurrent
// use: the pool calls them from N goroutines.
type Handler interface {
	EventType() string
	// IdempotencyKey declares the canonical handler-level identifier used for
	// event_key dedup and observability grouping. It MUST be non-empty; an
	// empty return panics at Register so a missing handler identity cannot
	// ship to production.
	//
	// The convention is a stable string shaped `<event_type>.<scheme>`
	// (e.g. "asset.index.requested.v1"). It is NOT the per-event idempotency
	// key inside the envelope payload; it ties a handler class to a dedup /
	// metrics namespace.
	IdempotencyKey() string
	Handle(ctx context.Context, evt Event) error
}

// HandlerRegistry maps an event type to its handler.
type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewHandlerRegistry returns an empty registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]Handler)}
}

// Register adds h. It rejects nil handlers, empty event types, empty
// idempotency keys (panic: fail-closed at init) and duplicate registrations.
func (r *HandlerRegistry) Register(h Handler) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h == nil {
		return fmt.Errorf("handler is nil")
	}
	key := h.EventType()
	if key == "" {
		return fmt.Errorf("handler event type is empty")
	}
	idemKey := h.IdempotencyKey()
	if idemKey == "" {
		panic(fmt.Sprintf("outbox.HandlerRegistry.Register: handler for %q returned empty IdempotencyKey() (fail-closed at init)", key))
	}
	if _, exists := r.handlers[key]; exists {
		return fmt.Errorf("handler already registered for %s", key)
	}
	r.handlers[key] = h
	return nil
}

// Get returns the handler registered for eventType.
func (r *HandlerRegistry) Get(eventType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[eventType]
	return h, ok
}

// Len reports how many handlers are registered.
func (r *HandlerRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers)
}

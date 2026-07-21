package adminconsoleapi

import "context"

// Event represents one admin console event streamed to clients.
type Event struct {
	Type    string         `json:"type"`
	ID      string         `json:"id,omitempty"`
	Payload map[string]any `json:"payload"`
}

// EventBus is the admin console event bus port.
// Implementations must be safe for concurrent use.
type EventBus interface {
	// Subscribe returns a channel that receives every event published
	// after the subscription is created.
	Subscribe(ctx context.Context) (<-chan Event, error)
	// Publish emits an event to all current subscribers.
	Publish(ctx context.Context, ev Event) error
}

// NoOpEventBus discards all events. It is the safe default until a
// real broker is wired.
type NoOpEventBus struct{}

// Subscribe implements EventBus by returning a channel that is
// immediately closed.
func (NoOpEventBus) Subscribe(ctx context.Context) (<-chan Event, error) {
	ch := make(chan Event)
	close(ch)
	return ch, nil
}

// Publish implements EventBus as a no-op.
func (NoOpEventBus) Publish(context.Context, Event) error { return nil }

// Compile-time check.
var _ EventBus = (*NoOpEventBus)(nil)

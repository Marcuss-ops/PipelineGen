package queue

import "context"

// Backend identifies the queue transport implementation.
type Backend string

const (
	BackendJSON         Backend = "json"
	BackendRedisStreams Backend = "redis-streams"
	BackendNATS         Backend = "nats"
	BackendNoop         Backend = "noop"
)

// Message is the normalized transport payload for async jobs.
type Message struct {
	ID       string            `json:"id"`
	Topic    string            `json:"topic"`
	JobID    string            `json:"job_id"`
	Payload  []byte            `json:"payload,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Attempt  int               `json:"attempt"`
	Consumer string            `json:"consumer,omitempty"`
}

// Lease represents ownership of a pending message.
type Lease struct {
	Message    Message
	LeaseToken string
}

// Queue is the transport contract that should replace queue.json in production.
type Queue interface {
	Backend() Backend
	Publish(ctx context.Context, msg Message) error
	LeaseNext(ctx context.Context, consumer string) (*Lease, error)
	Ack(ctx context.Context, lease Lease) error
	Nack(ctx context.Context, lease Lease, requeue bool) error
	Close() error
}

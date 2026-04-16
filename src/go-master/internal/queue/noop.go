package queue

import (
	"context"
	"errors"
)

var ErrNoQueueBackend = errors.New("no real queue backend configured")

// NoopQueue is a temporary fallback for dev/bootstrap paths.
// Production should use a real transport such as Redis Streams or NATS.
type NoopQueue struct{}

func NewNoopQueue() *NoopQueue { return &NoopQueue{} }

func (q *NoopQueue) Backend() Backend { return BackendNoop }

func (q *NoopQueue) Publish(ctx context.Context, msg Message) error {
	return ErrNoQueueBackend
}

func (q *NoopQueue) LeaseNext(ctx context.Context, consumer string) (*Lease, error) {
	return nil, ErrNoQueueBackend
}

func (q *NoopQueue) Ack(ctx context.Context, lease Lease) error {
	return ErrNoQueueBackend
}

func (q *NoopQueue) Nack(ctx context.Context, lease Lease, requeue bool) error {
	return ErrNoQueueBackend
}

func (q *NoopQueue) Close() error { return nil }

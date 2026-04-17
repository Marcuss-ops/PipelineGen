package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type mockQueue struct {
	messages []Message
	mu       sync.Mutex
	acks     int
	nacks    int
}

func (m *mockQueue) Backend() Backend { return BackendNoop }
func (m *mockQueue) Publish(ctx context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return nil
}
func (m *mockQueue) LeaseNext(ctx context.Context, consumer string) (*Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return nil, nil
	}
	msg := m.messages[0]
	m.messages = m.messages[1:]
	return &Lease{Message: msg, LeaseToken: "test"}, nil
}
func (m *mockQueue) Ack(ctx context.Context, lease Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acks++
	return nil
}
func (m *mockQueue) Nack(ctx context.Context, lease Lease, requeue bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nacks++
	if requeue {
		m.messages = append(m.messages, lease.Message)
	}
	return nil
}
func (m *mockQueue) Close() error { return nil }

func TestConsumer(t *testing.T) {
	q := &mockQueue{
		messages: []Message{
			{Topic: "topic1", JobID: "job1"},
			{Topic: "topic2", JobID: "job2"},
			{Topic: "error", JobID: "job3"},
		},
	}

	consumer := NewConsumer(q, "test-worker", 1)

	var wg sync.WaitGroup
	wg.Add(2)

	consumer.Register("topic1", func(ctx context.Context, msg Message) error {
		wg.Done()
		return nil
	})
	consumer.Register("topic2", func(ctx context.Context, msg Message) error {
		wg.Done()
		return nil
	})
	consumer.Register("error", func(ctx context.Context, msg Message) error {
		return errors.New("test error")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go consumer.Start(ctx)

	// Wait for the two successful jobs to be processed
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-ctx.Done():
		t.Fatal("Timeout waiting for jobs")
	}

	// Wait a bit more for the error job to be nacked
	time.Sleep(100 * time.Millisecond)

	q.mu.Lock()
	if q.acks != 2 {
		t.Errorf("Expected 2 acks, got %d", q.acks)
	}
	if q.nacks < 1 {
		t.Errorf("Expected at least 1 nack, got %d", q.nacks)
	}
	q.mu.Unlock()

	consumer.Stop()
}

package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"
)

type outboxDispatchDiscoveries struct {
	markFailedErr     error
	markDispatchedErr error
	failedCalls       int
	dispatchedCalls   int
}

func (d *outboxDispatchDiscoveries) TryReserve(context.Context, string, string, string, string, string, string) (string, bool, int, error) {
	return "", false, 0, nil
}
func (d *outboxDispatchDiscoveries) MarkEnqueued(context.Context, string, string) error { return nil }
func (d *outboxDispatchDiscoveries) MarkRejected(context.Context, string, string, bool) error {
	return nil
}
func (d *outboxDispatchDiscoveries) MaxDiscoveredAt(context.Context, string) (string, error) {
	return "", nil
}
func (d *outboxDispatchDiscoveries) CommitEnqueueOutbox(context.Context, string, string, string, string) error {
	return nil
}
func (d *outboxDispatchDiscoveries) DrainPendingOutbox(context.Context, int, string, string) ([]OutboxEntry, error) {
	return nil, nil
}
func (d *outboxDispatchDiscoveries) DrainDispatched(context.Context, int, string, string) ([]OutboxEntry, error) {
	return nil, nil
}
func (d *outboxDispatchDiscoveries) MarkOutboxDispatched(context.Context, int64, string) error {
	d.dispatchedCalls++
	return d.markDispatchedErr
}
func (d *outboxDispatchDiscoveries) MarkOutboxFailed(context.Context, int64, string) error {
	d.failedCalls++
	return d.markFailedErr
}

var _ YoutubeDiscoveriesPort = (*outboxDispatchDiscoveries)(nil)

type outboxDispatchEnqueuer struct {
	err   error
	calls int
}

func (e *outboxDispatchEnqueuer) EnqueueExtract(context.Context, EnqueueExtractRequest) error {
	e.calls++
	return e.err
}

var _ JobEnqueuer = (*outboxDispatchEnqueuer)(nil)

func outboxEntryPayload(t *testing.T) string {
	t.Helper()
	payload, err := json.Marshal(EnqueueExtractRequest{VideoID: "video-1", Title: "title"})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestDispatchOutboxEntryPreservesEnqueueAndMarkFailedErrors(t *testing.T) {
	enqueueErr := errors.New("enqueue failed")
	markErr := errors.New("mark failed persistence failed")
	discoveries := &outboxDispatchDiscoveries{markFailedErr: markErr}
	monitor := &ChannelMonitor{
		log:         zap.NewNop(),
		discoveries: discoveries,
		enqueuer:    &outboxDispatchEnqueuer{err: enqueueErr},
	}

	err := monitor.dispatchOutboxEntry(context.Background(), OutboxEntry{ID: 7, PayloadJSON: outboxEntryPayload(t)})

	if !errors.Is(err, enqueueErr) {
		t.Fatalf("error = %v, want enqueue cause", err)
	}
	if !errors.Is(err, markErr) {
		t.Fatalf("error = %v, want MarkOutboxFailed cause", err)
	}
	if discoveries.failedCalls != 1 {
		t.Fatalf("MarkOutboxFailed calls = %d, want 1", discoveries.failedCalls)
	}
}

func TestDispatchOutboxEntryPreservesMarkDispatchedError(t *testing.T) {
	markErr := errors.New("mark dispatched persistence failed")
	discoveries := &outboxDispatchDiscoveries{markDispatchedErr: markErr}
	enqueuer := &outboxDispatchEnqueuer{}
	monitor := &ChannelMonitor{
		log:         zap.NewNop(),
		discoveries: discoveries,
		enqueuer:    enqueuer,
	}

	err := monitor.dispatchOutboxEntry(context.Background(), OutboxEntry{ID: 8, PayloadJSON: outboxEntryPayload(t)})

	if !errors.Is(err, markErr) {
		t.Fatalf("error = %v, want MarkOutboxDispatched cause", err)
	}
	if enqueuer.calls != 1 {
		t.Fatalf("EnqueueExtract calls = %d, want 1", enqueuer.calls)
	}
}

func TestDispatchOutboxEntryMarksMalformedPayloadFailed(t *testing.T) {
	markErr := errors.New("mark malformed payload failed")
	discoveries := &outboxDispatchDiscoveries{markFailedErr: markErr}
	monitor := &ChannelMonitor{
		log:         zap.NewNop(),
		discoveries: discoveries,
		enqueuer:    &outboxDispatchEnqueuer{},
	}

	err := monitor.dispatchOutboxEntry(context.Background(), OutboxEntry{ID: 9, PayloadJSON: "{"})

	if err == nil {
		t.Fatal("dispatchOutboxEntry returned nil for malformed payload")
	}
	if !errors.Is(err, markErr) {
		t.Fatalf("error = %v, want MarkOutboxFailed cause", err)
	}
	if discoveries.failedCalls != 1 {
		t.Fatalf("MarkOutboxFailed calls = %d, want 1", discoveries.failedCalls)
	}
}

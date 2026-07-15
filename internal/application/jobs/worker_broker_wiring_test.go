package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

type mockBroker struct {
	calls     int
	lastCmd   CompleteWithArtifactsCommand
	returnErr error
	returnIDs []string
}

func (m *mockBroker) CompleteWithArtifacts(_ context.Context, cmd CompleteWithArtifactsCommand) ([]string, error) {
	m.calls++
	m.lastCmd = cmd
	return m.returnIDs, m.returnErr
}

var _ CompletionPort = (*mockBroker)(nil)

func newBrokerTestWorker(t *testing.T, id string) *Worker {
	t.Helper()
	return NewWorkerFromDeps(WorkerDeps{
		Identity: WorkerIdentityDeps{ID: id},
		Runtime:  WorkerRuntimeDeps{},
		Timing: WorkerTimingDeps{
			LeaseTTL:  time.Minute,
			PollEvery: time.Second,
			Backoff:   BackoffConfig{},
		},
		Log: zaptest.NewLogger(t),
	})
}

func TestWorker_WithBroker_AttachesCompletionPort(t *testing.T) {
	mock := &mockBroker{}
	worker := newBrokerTestWorker(t, "test-worker-"+t.Name())

	returned := worker.WithBroker(mock)
	if returned != worker {
		t.Fatalf("WithBroker must return receiver; got %p, want %p", returned, worker)
	}
	if worker.broker != mock {
		t.Fatalf("Worker.broker=%p, want %p", worker.broker, mock)
	}
}

func TestWorker_NewWorker_DefaultBrokerNil(t *testing.T) {
	worker := newBrokerTestWorker(t, "test-worker-default-"+t.Name())
	if worker.broker != nil {
		t.Fatalf("new worker must leave broker nil; got %v", worker.broker)
	}
}

func TestCompletionPort_PropagatesCommandEnvelope(t *testing.T) {
	mock := &mockBroker{returnIDs: []string{"asset_abc", "asset_def"}}
	var completion CompletionPort = mock
	want := CompleteWithArtifactsCommand{
		WorkerID:         "w-id",
		JobID:            "job-id",
		LeaseID:          "lease-id",
		ExpectedRevision: 7,
		CorrelationID:    "corr-id",
		ResultData:       json.RawMessage(`{"ok":true}`),
		StagedArtifacts:  json.RawMessage("[]"),
	}

	got, err := completion.CompleteWithArtifacts(context.Background(), want)
	if err != nil {
		t.Fatalf("CompleteWithArtifacts error: %v", err)
	}
	if mock.calls != 1 {
		t.Fatalf("calls=%d, want 1", mock.calls)
	}
	if mock.lastCmd.WorkerID != want.WorkerID ||
		mock.lastCmd.JobID != want.JobID ||
		mock.lastCmd.LeaseID != want.LeaseID ||
		mock.lastCmd.ExpectedRevision != want.ExpectedRevision ||
		mock.lastCmd.CorrelationID != want.CorrelationID {
		t.Fatalf("command envelope drift: got %+v want %+v", mock.lastCmd, want)
	}
	if len(got) != 2 || got[0] != "asset_abc" || got[1] != "asset_def" {
		t.Fatalf("return IDs=%v, want [asset_abc asset_def]", got)
	}
}

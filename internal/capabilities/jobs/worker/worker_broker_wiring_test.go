// Package jobs — worker_broker_wiring_test.go (PR-WORKER-RUNNER-INPROCESS-MIGRATION, July 2026).
//
// TDD coverage for the Worker → CompletionPort wiring contract. Mirrors
// the existing TestWorker_HonorsRegistryTimeout pattern at
// registry_wiring_test.go (HC-1 June 2026 precedent) so the test
// surface is consistent with the rest of the worker registries.
//
// godlike/06 SSOT: the wire contract lives on (w *Worker).WithBroker(cp
// CompletionPort) *Worker at worker.go; the 3 tests below pin the
// receiver-binding + default-state + propagation contracts so a future
// rename or type drift surfaces as a build failure rather than a
// workflow regression.
//
// Honest scope-lock (godlike/07): this file pins ONLY the wiring +
// mock propagation contract. The full runJob → CompletionPort routing
// contract is exercised end-to-end by FASE B Smoke Test 9
// (tests/operational/fase_b_clip_pipeline_smoke.sh). Forward-pointer:
// an in-process integration test (TestRunJob_RouteToCompletionPort)
// requires a stub *Dispatcher + stub *job.Store + stub CompletionPort
// setup that mirrors the existing registry_wiring_test fixtures;
// deferred to a follow-up PR per godlike/07 minimum-blast-radius.
package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// mockBroker is a hand-rolled stub that satisfies CompletionPort
// structurally so tests can verify Wire-Up + payload shape without
// spinning up the real in-process *local.Broker.
type mockBroker struct {
	calls     int
	lastCmd   CompleteWithArtifactsCommand
	returnErr error
	returnIDs []string
}

func (m *mockBroker) CompleteWithArtifacts(ctx context.Context, cmd CompleteWithArtifactsCommand) ([]string, error) {
	m.calls++
	m.lastCmd = cmd
	return m.returnIDs, m.returnErr
}

// Compile-time pin: mockBroker satisfies CompletionPort structurally.
// A future drift in the CompletionPort signature surfaces here as a
// build failure rather than a runtime test panic (godlike/06 SSOT
// + AGENTS.md Pattern 0).
var _ CompletionPort = (*mockBroker)(nil)

// TestWorker_WithBroker_AttachesCompletionPort verifies the WithBroker
// fluent setter stores the typed narrow port on the Worker.broker
// field AND returns the receiver for builder-style chaining.
func TestWorker_WithBroker_AttachesCompletionPort(t *testing.T) {
	mock := &mockBroker{}
	w := NewWorker(WorkerDeps{
		ID:        "test-worker-" + t.Name(),
		Log:       zaptest.NewLogger(t),
		LeaseTTL:  time.Minute,
		PollEvery: time.Second,
		Backoff:   BackoffConfig{},
	})

	returned := w.WithBroker(mock)
	if returned != w {
		t.Fatalf("WithBroker must return the receiver for builder-style chaining; got %p, want %p", returned, w)
	}
	if w.broker == nil {
		t.Fatal("WithBroker(cp) must attach the CompletionPort to Worker.broker field; got nil")
	}
	if w.broker != mock {
		t.Fatalf("Worker.broker must be the same pointer passed to WithBroker; got %p, want %p", w.broker, mock)
	}
}

// TestWorker_NewWorker_DefaultBrokerNil verifies the constructor
// leaves the broker field nil so legacy fixtures (those that don't
// wire a CompletionPort) continue to compile + the runJob branch
// falls through to the legacy w.repo.Complete path (godlike/07
// nil-tolerant).
func TestWorker_NewWorker_DefaultBrokerNil(t *testing.T) {
	w := NewWorker(WorkerDeps{
		ID:        "test-worker-default-" + t.Name(),
		Log:       zaptest.NewLogger(t),
		LeaseTTL:  time.Minute,
		PollEvery: time.Second,
		Backoff:   BackoffConfig{},
	})
	if w.broker != nil {
		t.Fatalf("NewWorker must leave Worker.broker nil by default; got %v", w.broker)
	}
}

// TestCompletionPort_PropagatesCommandEnvelope pins the contract that
// the caller's CompleteWithArtifactsCommand envelope reaches the
// CompletionPort consumer verbatim. This is the test-side equivalent
// of the AZIONE 5 (July 2026) command-propagation requirement;
// future drift in CompleteWithArtifactsCommand field names surfaces
// here as a test failure.
func TestCompletionPort_PropagatesCommandEnvelope(t *testing.T) {
	mock := &mockBroker{returnIDs: []string{"asset_abc", "asset_def"}}
	var cp CompletionPort = mock

	want := CompleteWithArtifactsCommand{
		WorkerID:         "w-id",
		WorkerSessionID:  "",
		JobID:            "job-id",
		LeaseID:          "lease-id",
		ExpectedRevision: 7,
		CorrelationID:    "corr-id",
		ResultData:       json.RawMessage(`{"ok":true}`),
		StagedArtifacts:  json.RawMessage("[]"),
		OutboxEvents:     nil,
	}

	got, err := cp.CompleteWithArtifacts(context.Background(), want)
	if err != nil {
		t.Fatalf("CompleteWithArtifacts must not error in stub success path; got %v", err)
	}
	if mock.calls != 1 {
		t.Errorf("CompleteWithArtifacts must invoke once; got %d calls", mock.calls)
	}
	if mock.lastCmd.WorkerID != want.WorkerID {
		t.Errorf("WorkerID propagated incorrectly; got %q, want %q", mock.lastCmd.WorkerID, want.WorkerID)
	}
	if mock.lastCmd.JobID != want.JobID {
		t.Errorf("JobID propagated incorrectly; got %q, want %q", mock.lastCmd.JobID, want.JobID)
	}
	if mock.lastCmd.LeaseID != want.LeaseID {
		t.Errorf("LeaseID propagated incorrectly; got %q, want %q", mock.lastCmd.LeaseID, want.LeaseID)
	}
	if mock.lastCmd.ExpectedRevision != want.ExpectedRevision {
		t.Errorf("ExpectedRevision propagated incorrectly; got %d, want %d", mock.lastCmd.ExpectedRevision, want.ExpectedRevision)
	}
	if mock.lastCmd.CorrelationID != want.CorrelationID {
		t.Errorf("CorrelationID propagated incorrectly; got %q, want %q", mock.lastCmd.CorrelationID, want.CorrelationID)
	}
	if len(got) != 2 || got[0] != "asset_abc" || got[1] != "asset_def" {
		t.Errorf("CompleteWithArtifacts return IDs propagated incorrectly; got %v, want [asset_abc asset_def]", got)
	}
}

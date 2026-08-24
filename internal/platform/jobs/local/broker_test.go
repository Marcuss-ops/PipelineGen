// Tests for internal/infrastructure/jobs/local/broker.go — W1 Phase 5 spec checklist.
//
// Phase 5 of docs/worker/W1_REMOTE_WORKER_GATE.md requires the broker to
// FAIL CLOSED on empty capabilities (both at registration and at claim time).
// Earlier work shipped a silent no-op (Claim returning nil, nil for empty
// caps) which let unconfigured workers loop forever and starve the queue
// without surfacing the misconfiguration. These tests pin the new sentinel:
// appjobs.ErrNoWorkerCapabilities must come back from both entry points.
//
// We deliberately use an UNCONFIGURED Broker (&Broker{}) — neither workers
// nor jobs.Store are wired — because the guard fires before any of the
// nil-dep branches. Practically this is the state a unit test produces
// without bringing up the full SQLite-backed worker repository.
package local

import (
	"context"
	"errors"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
)

func TestBroker_RegisterWorker_RejectsEmptyCapabilities(t *testing.T) {
	b := &Broker{} // nil workers, nil jobs — guards fire before deps are touched
	_, err := b.RegisterWorker(context.Background(), appjobs.RegisterWorkerCommand{
		WorkerID:     "w-test-empty",
		Capabilities: appjobs.WorkerCapabilities{JobTypes: nil},
	})
	if err == nil {
		t.Fatal("expected error for empty advertised capabilities at registration")
	}
	if !errors.Is(err, appjobs.ErrNoWorkerCapabilities) {
		t.Fatalf("expected ErrNoWorkerCapabilities, got %v", err)
	}
}

func TestBroker_RegisterWorker_RejectsEmptyCapabilitiesArray(t *testing.T) {
	b := &Broker{}
	_, err := b.RegisterWorker(context.Background(), appjobs.RegisterWorkerCommand{
		WorkerID:     "w-test-empty-arr",
		Capabilities: appjobs.WorkerCapabilities{JobTypes: []string{}},
	})
	if !errors.Is(err, appjobs.ErrNoWorkerCapabilities) {
		t.Fatalf("expected ErrNoWorkerCapabilities for explicit empty array, got %v", err)
	}
}

func TestBroker_Claim_RejectsEmptyCapabilities(t *testing.T) {
	b := &Broker{}
	_, err := b.Claim(context.Background(), appjobs.ClaimCommand{
		WorkerID:        "w-test-empty",
		WorkerSessionID: "sess-x",
		Capabilities:    nil,
	})
	if err == nil {
		t.Fatal("expected error for empty advertised capabilities at claim time")
	}
	if !errors.Is(err, appjobs.ErrNoWorkerCapabilities) {
		t.Fatalf("expected ErrNoWorkerCapabilities, got %v", err)
	}
}

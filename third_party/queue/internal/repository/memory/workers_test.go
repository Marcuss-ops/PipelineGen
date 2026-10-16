package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestWorkerRegisterHeartbeatList(t *testing.T) {
	r := New(30*time.Second, 3)

	if err := r.Register(context.Background(), model.Worker{ID: "w1", Hostname: "h1", Status: model.WorkerStatusReady}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(context.Background(), model.Worker{ID: "w2", Status: model.WorkerStatusBusy}); err != nil {
		t.Fatal(err)
	}

	workers, err := r.List(context.Background())
	if err != nil || len(workers) != 2 {
		t.Fatalf("list: got %d workers, err=%v", len(workers), err)
	}
	if workers[0].ID != "w1" || workers[0].Hostname != "h1" {
		t.Fatalf("worker w1 not round-tripped: %+v", workers[0])
	}

	if workers[0].LastHeartbeatAt.IsZero() {
		t.Fatal("register must record an initial heartbeat")
	}
	// The store does NOT classify liveness: it returns rows. The derived field
	// must be empty here, because a backend that filled it would be a second
	// authority for "is this worker alive?" (model.ClassifyWorkerLiveness).
	for _, w := range workers {
		if w.Liveness != "" {
			t.Fatalf("store fabricated liveness %q for %s", w.Liveness, w.ID)
		}
	}

	if err := r.Heartbeat(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	workers, err = r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if workers[0].LastHeartbeatAt.Before(workers[0].StartedAt) {
		t.Fatal("heartbeat must not move the last heartbeat before the start time")
	}
}

func TestWorkerHeartbeatUnregisteredFails(t *testing.T) {
	r := New(30*time.Second, 3)
	if err := r.Heartbeat(context.Background(), "missing"); err == nil {
		t.Fatal("heartbeat on unregistered worker should fail")
	}
}

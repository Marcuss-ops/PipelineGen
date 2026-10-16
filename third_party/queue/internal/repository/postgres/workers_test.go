package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestWorkerRegisterHeartbeatList(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if _, err := db.ExecContext(context.Background(), `TRUNCATE rendering_workers CASCADE`); err != nil {
		t.Fatal(err)
	}

	if err := r.Register(context.Background(), model.Worker{ID: "w1", Hostname: "h1", Status: model.WorkerStatusReady, RenderingGenVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(context.Background(), model.Worker{ID: "w2", Status: model.WorkerStatusBusy}); err != nil {
		t.Fatal(err)
	}

	if err := r.Heartbeat(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	workers, err := r.List(context.Background())
	if err != nil || len(workers) != 2 {
		t.Fatalf("list: got %d workers, err=%v", len(workers), err)
	}
	if workers[0].ID != "w1" || workers[0].Hostname != "h1" || workers[0].RenderingGenVersion != "v1" {
		t.Fatalf("worker w1 not round-tripped: %+v", workers[0])
	}
	if workers[0].LastHeartbeatAt.IsZero() {
		t.Fatal("w1 should have a heartbeat timestamp")
	}

	// The store does NOT classify liveness (there is deliberately no Health
	// method on the contract): it returns the stored status and heartbeat, and
	// model.ClassifyWorkerLiveness is the only thing that turns them into a
	// verdict. A row that arrived with a derived liveness would be a second
	// authority for the same question.
	if workers[0].Liveness != "" {
		t.Fatalf("store fabricated liveness %q for %s", workers[0].Liveness, workers[0].ID)
	}

	// A heartbeat row must have been appended to the ledger.
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM worker_heartbeats WHERE worker_id = 'w1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 heartbeat row for w1, got %d", count)
	}
}

func TestWorkerHeartbeatUnregisteredFails(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if err := r.Heartbeat(context.Background(), "missing"); err == nil {
		t.Fatal("heartbeat on unregistered worker should fail")
	}
}

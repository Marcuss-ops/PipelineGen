package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

func TestNewService_NilRegistry_ReturnsErrRegistryRequired(t *testing.T) {
	t.Parallel()
	svc, err := NewService(nil, nil, zap.NewNop(), nil)
	if err == nil || !errors.Is(err, ErrRegistryRequired) || svc != nil {
		t.Fatalf("NewService(nil registry) = (%v, %v), want nil + ErrRegistryRequired", svc, err)
	}
}

func TestNewService_HappyPath_ReturnsService(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 7)
	svc, err := NewService(nakedJobBroker{}, NewDispatcher(), zap.NewNop(), reg)
	if err != nil || svc == nil {
		t.Fatalf("NewService() = (%v, %v)", svc, err)
	}
	if svc.registry != reg {
		t.Fatal("service registry mismatch")
	}
}

func TestEnqueue_HappyPath_PopulatesMaxRetriesFromRegistry(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 7)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: wiringTestType, Priority: 5, Payload: map[string]any{"hello": "world"}})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got == nil || got.MaxRetries != 7 {
		t.Fatalf("Enqueue() = %#v, want MaxRetries=7", got)
	}
}

func TestEnqueue_ExistingCorrelationID_DedupReturnsExisting(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 3)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	req := &job.EnqueueRequest{Type: wiringTestType, CorrelationID: "cid-dedup", Payload: map[string]any{"run": 1}}
	first, err := svc.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	req2 := *req
	req2.Payload = map[string]any{"run": 2}
	second, err := svc.Enqueue(context.Background(), &req2)
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if first == nil || second == nil || first.ID != second.ID {
		t.Fatalf("dedup mismatch: first=%#v second=%#v", first, second)
	}
}

func TestEnqueue_PopulatesRootJobID_CanonicalLineage(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 3)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	root, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: wiringTestType, Payload: map[string]any{"item": "root"}})
	if err != nil {
		t.Fatalf("root Enqueue: %v", err)
	}
	if root.RootJobID != root.ID || root.ParentJobID != "" {
		t.Fatalf("root lineage = %#v", root)
	}
	child, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: wiringTestType, Payload: map[string]any{"parent_job_id": root.ID}})
	if err != nil {
		t.Fatalf("child Enqueue: %v", err)
	}
	if child.ParentJobID != root.ID || child.RootJobID != root.ID {
		t.Fatalf("child lineage = %#v", child)
	}
}

func TestErrUniqueConstraintViolation_IsKernelDuplicate(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrUniqueConstraintViolation, job.ErrDuplicate) {
		t.Fatalf("compat sentinel must alias kernel job.ErrDuplicate")
	}
}

func TestEnqueue_M2M_IdempotencyIsScopedPerClient(t *testing.T) {
	t.Parallel()
	reg := newWiringRegistry(t, time.Minute, 7)
	store, cleanup := newSqliteStoreForTest(t)
	defer cleanup()
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())
	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	first, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: wiringTestType, ClientID: "client-A", IdempotencyKey: "render-001"})
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	same, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: wiringTestType, ClientID: "client-A", IdempotencyKey: "render-001"})
	if err != nil {
		t.Fatalf("same-key Enqueue: %v", err)
	}
	other, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: wiringTestType, ClientID: "client-B", IdempotencyKey: "render-001"})
	if err != nil {
		t.Fatalf("other-client Enqueue: %v", err)
	}
	if first.ID != same.ID {
		t.Fatalf("same client/key did not dedup: %s != %s", first.ID, same.ID)
	}
	if first.ID == other.ID {
		t.Fatalf("different clients collided on idempotency key: %s", first.ID)
	}
}

// Shared by worker_observability_test.go.
func newSqliteStoreForTest(t *testing.T) (*sqljobs.SQLiteStore, func()) {
	t.Helper()
	db := setupTestDB(t)
	return sqljobs.NewSQLiteStore(db, zap.NewNop()), func() {}
}

package jobs

import (
	"context"
	"database/sql"
	"testing"

	"go.uber.org/zap"
)

func setupTestStoreWithEvents(t *testing.T) (*SQLiteStore, *SQLiteEventsStore, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	schema := `
		CREATE TABLE jobs_new (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'created',
			payload_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT NOT NULL DEFAULT '{}',
			error TEXT DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 3,
			created_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE job_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL
		);
	`
	_, err = db.Exec(schema)
	if err != nil {
		t.Fatalf("failed to create tables: %v", err)
	}

	store := NewSQLiteStore(db)
	eventsStore := NewSQLiteEventsStore(db)
	return store, eventsStore, func() { db.Close() }
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()

	handlerCalled := false
	registry.Register("test_job", func(ctx context.Context, payload string) (any, error) {
		handlerCalled = true
		return "done", nil
	})

	handler, ok := registry.Get("test_job")
	if !ok {
		t.Fatal("expected handler to be registered")
	}

	_, err := handler(context.Background(), "{}")
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if !handlerCalled {
		t.Error("expected handler to be called")
	}

	_, ok = registry.Get("non_existent")
	if ok {
		t.Error("expected no handler for non_existent type")
	}
}

func TestRunnerProcessJob(t *testing.T) {
	store, _, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	registry := NewRegistry()
	logger := zap.NewNop()

	job := &Job{
		ID:     "job-001",
		Type:   "test_job",
		Status: JobStatusQueued,
	}
	store.Create(context.Background(), job)

	registry.Register("test_job", func(ctx context.Context, payload string) (any, error) {
		return map[string]string{"result": "success"}, nil
	})

	runner := NewRunner(store, registry, logger)
	leased, err := store.LeaseNext(context.Background())
	if err != nil {
		t.Fatalf("LeaseNext failed: %v", err)
	}

	if leased == nil {
		t.Fatal("expected job to be leased")
	}

	runner.processJob(context.Background(), leased)

	updated, _ := store.Get(context.Background(), "job-001")
	if updated.Status != JobStatusSucceeded {
		t.Errorf("expected succeeded, got %s", updated.Status)
	}
}

func TestRunnerProcessJobFailure(t *testing.T) {
	store, _, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	registry := NewRegistry()
	logger := zap.NewNop()

	job := &Job{
		ID:         "job-001",
		Type:       "test_job",
		Status:     JobStatusQueued,
		MaxAttempts: 3,
	}
	store.Create(context.Background(), job)

	registry.Register("test_job", func(ctx context.Context, payload string) (any, error) {
		return nil, context.DeadlineExceeded
	})

	runner := NewRunner(store, registry, logger)
	leased, _ := store.LeaseNext(context.Background())
	runner.processJob(context.Background(), leased)

	updated, _ := store.Get(context.Background(), "job-001")
	if updated.Status != JobStatusQueued {
		t.Errorf("expected queued (for retry), got %s", updated.Status)
	}
	if updated.Attempts != 1 {
		t.Errorf("expected attempts=1, got %d", updated.Attempts)
	}
}

func TestRunnerNoHandler(t *testing.T) {
	store, _, cleanup := setupTestStoreWithEvents(t)
	defer cleanup()

	registry := NewRegistry()
	logger := zap.NewNop()

	job := &Job{
		ID:     "job-001",
		Type:   "unknown_type",
		Status: JobStatusQueued,
	}
	store.Create(context.Background(), job)

	runner := NewRunner(store, registry, logger)
	leased, _ := store.LeaseNext(context.Background())
	runner.processJob(context.Background(), leased)

	updated, _ := store.Get(context.Background(), "job-001")
	if updated.Status != JobStatusFailed {
		t.Errorf("expected failed, got %s", updated.Status)
	}
}

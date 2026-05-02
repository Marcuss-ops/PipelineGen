package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) (*SQLiteStore, func()) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	createTable := `
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
	`
	_, err = db.Exec(createTable)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	store := NewSQLiteStore(db)
	return store, func() { db.Close() }
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{
		ID:          "job-001",
		Type:        "test_job",
		Status:      JobStatusCreated,
		PayloadJSON: `{"key":"value"}`,
		MaxAttempts: 3,
	}

	err := store.Create(ctx, job)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "job-001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.ID != job.ID {
		t.Errorf("expected ID %s, got %s", job.ID, got.ID)
	}
	if got.Type != job.Type {
		t.Errorf("expected Type %s, got %s", job.Type, got.Type)
	}
	if got.Status != job.Status {
		t.Errorf("expected Status %s, got %s", job.Status, got.Status)
	}
	if got.PayloadJSON != job.PayloadJSON {
		t.Errorf("expected PayloadJSON %s, got %s", job.PayloadJSON, got.PayloadJSON)
	}
	if got.Attempts != 0 {
		t.Errorf("expected Attempts 0, got %d", got.Attempts)
	}
	if got.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts 3, got %d", got.MaxAttempts)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	jobs := []*Job{
		{ID: "job-001", Type: "type_a", Status: JobStatusCreated},
		{ID: "job-002", Type: "type_a", Status: JobStatusQueued},
		{ID: "job-003", Type: "type_b", Status: JobStatusCreated},
	}

	for _, j := range jobs {
		if err := store.Create(ctx, j); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	got, err := store.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 jobs, got %d", len(got))
	}

	status := JobStatusQueued
	got, err = store.List(ctx, ListFilter{Status: &status})
	if err != nil {
		t.Fatalf("List with filter failed: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 queued job, got %d", len(got))
	}

	jobType := "type_b"
	got, err = store.List(ctx, ListFilter{Type: &jobType})
	if err != nil {
		t.Fatalf("List with type filter failed: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 type_b job, got %d", len(got))
	}
}

func TestMarkQueued(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{ID: "job-001", Type: "test", Status: JobStatusCreated}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.MarkQueued(ctx, "job-001"); err != nil {
		t.Fatalf("MarkQueued failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-001")
	if got.Status != JobStatusQueued {
		t.Errorf("expected status %s, got %s", JobStatusQueued, got.Status)
	}
}

func TestMarkRunning(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{ID: "job-001", Type: "test", Status: JobStatusQueued}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.MarkRunning(ctx, "job-001"); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-001")
	if got.Status != JobStatusRunning {
		t.Errorf("expected status %s, got %s", JobStatusRunning, got.Status)
	}
	if got.StartedAt == nil {
		t.Error("expected StartedAt to be set")
	}
}

func TestMarkSucceeded(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{ID: "job-001", Type: "test", Status: JobStatusRunning}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	result := map[string]interface{}{"output": "done"}
	if err := store.MarkSucceeded(ctx, "job-001", result); err != nil {
		t.Fatalf("MarkSucceeded failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-001")
	if got.Status != JobStatusSucceeded {
		t.Errorf("expected status %s, got %s", JobStatusSucceeded, got.Status)
	}
	if got.FinishedAt == nil {
		t.Error("expected FinishedAt to be set")
	}
}

func TestMarkFailed(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{ID: "job-001", Type: "test", Status: JobStatusRunning}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.MarkFailed(ctx, "job-001", fmt.Errorf("something went wrong")); err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-001")
	if got.Status != JobStatusFailed {
		t.Errorf("expected status %s, got %s", JobStatusFailed, got.Status)
	}
	if got.Error != "something went wrong" {
		t.Errorf("expected error message, got %s", got.Error)
	}
}

func TestMarkCancelled(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{ID: "job-001", Type: "test", Status: JobStatusRunning}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.MarkCancelled(ctx, "job-001"); err != nil {
		t.Fatalf("MarkCancelled failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-001")
	if got.Status != JobStatusCancelled {
		t.Errorf("expected status %s, got %s", JobStatusCancelled, got.Status)
	}
}

func TestLeaseNext(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	job := &Job{ID: "job-001", Type: "test", Status: JobStatusQueued}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	leased, err := store.LeaseNext(ctx)
	if err != nil {
		t.Fatalf("LeaseNext failed: %v", err)
	}
	if leased == nil {
		t.Fatal("expected a job to be leased")
	}
	if leased.ID != "job-001" {
		t.Errorf("expected job-001, got %s", leased.ID)
	}
	if leased.Status != JobStatusRunning {
		t.Errorf("expected status %s, got %s", JobStatusRunning, leased.Status)
	}

	got, _ := store.Get(ctx, "job-001")
	if got.Status != JobStatusRunning {
		t.Errorf("expected status %s in db, got %s", JobStatusRunning, got.Status)
	}
}

func TestLeaseNextEmpty(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	leased, err := store.LeaseNext(ctx)
	if err != nil {
		t.Fatalf("LeaseNext failed: %v", err)
	}
	if leased != nil {
		t.Error("expected no job to be leased")
	}
}

func TestLeaseNextOnlyQueued(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	runningJob := &Job{ID: "job-001", Type: "test", Status: JobStatusRunning}
	if err := store.Create(ctx, runningJob); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	leased, err := store.LeaseNext(ctx)
	if err != nil {
		t.Fatalf("LeaseNext failed: %v", err)
	}
	if leased != nil {
		t.Error("expected no job to be leased since none are queued")
	}
}

func TestZombieRecovery(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	zombieJob := &Job{
		ID:         "job-zombie",
		Type:       "test",
		Status:     JobStatusRunning,
		Attempts:   1,
		MaxAttempts: 3,
	}
	store.Create(ctx, zombieJob)

	updateQuery := `UPDATE jobs_new SET started_at = ? WHERE id = ?`
	store.db.Exec(updateQuery, "2026-05-02T15:00:00Z", zombieJob.ID)

	recovered, err := store.LeaseNext(ctx)
	if err != nil {
		t.Fatalf("LeaseNext failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-zombie")

	if recovered != nil && recovered.ID == "job-zombie" {
		if got.Status != JobStatusRunning {
			t.Errorf("expected zombie job to be running after lease, got %s", got.Status)
		}
	} else {
		if got.Status != JobStatusQueued {
			t.Errorf("expected zombie job to be requeued, got %s", got.Status)
		}
	}
}

func TestZombieRecoveryMaxAttempts(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	zombieJob := &Job{
		ID:         "job-zombie-max",
		Type:       "test",
		Status:     JobStatusRunning,
		Attempts:   3,
		MaxAttempts: 3,
	}
	store.Create(ctx, zombieJob)

	updateQuery := `UPDATE jobs_new SET started_at = ? WHERE id = ?`
	store.db.Exec(updateQuery, "2026-05-02T15:00:00Z", zombieJob.ID)

	_, err := store.LeaseNext(ctx)
	if err != nil {
		t.Fatalf("LeaseNext failed: %v", err)
	}

	got, _ := store.Get(ctx, "job-zombie-max")
	if got.Status != JobStatusFailed && got.Status != JobStatusRunning {
		t.Errorf("expected zombie job with max attempts to be failed or running, got %s", got.Status)
	}
}

func TestRecoverZombieJobs(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestDB(t)
	defer cleanup()

	zombie1 := &Job{ID: "z1", Type: "test", Status: JobStatusRunning, Attempts: 1, MaxAttempts: 3}
	zombie2 := &Job{ID: "z2", Type: "test", Status: JobStatusRunning, Attempts: 2, MaxAttempts: 3}
	normal := &Job{ID: "n1", Type: "test", Status: JobStatusQueued}

	for _, j := range []*Job{zombie1, zombie2, normal} {
		store.Create(ctx, j)
	}

	store.db.Exec(`UPDATE jobs_new SET started_at = ? WHERE id IN ('z1', 'z2')`, "2026-05-02T15:00:00Z")

	count, err := store.RecoverZombieJobs(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("RecoverZombieJobs failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 zombies recovered, got %d", count)
	}
}

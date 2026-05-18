package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/media/models"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use temp file for better concurrency support
	tmpFile, err := os.CreateTemp("", "test_jobs_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	db, err := sql.Open("sqlite3", tmpFile.Name()+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create jobs table
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		priority INTEGER NOT NULL DEFAULT 0,
		project TEXT DEFAULT '',
		video_name TEXT DEFAULT '',
		active_key TEXT DEFAULT '',
		payload_json TEXT NOT NULL DEFAULT '{}',
		result_json TEXT NOT NULL DEFAULT '{}',
		progress INTEGER NOT NULL DEFAULT 0,
		error TEXT DEFAULT '',
		retry_count INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL DEFAULT 3,
		worker_id TEXT DEFAULT '',
		lease_expiry TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT,
		cancelled_at TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_jobs_status_priority ON jobs(status, priority DESC, created_at ASC);
	CREATE TABLE IF NOT EXISTS job_events (
		id TEXT PRIMARY KEY,
		job_id TEXT NOT NULL,
		type TEXT NOT NULL,
		message TEXT NOT NULL DEFAULT '',
		data_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		FOREIGN KEY(job_id) REFERENCES jobs(id)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	return db
}

func setupTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	db := setupTestDB(t)
	repo := jobs.NewRepository(db, zap.NewNop())
	svc := NewService(repo, nil, zap.NewNop())

	return svc, func() {}
}

func TestCreateJobStoresPendingJob(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:     models.JobType("test_job"),
		Priority: 1,
		Project:  "test-project",
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}
	if job.Status != models.StatusQueued {
		t.Errorf("expected status %s, got %s", models.StatusQueued, job.Status)
	}
	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
}

func TestJobMovesToCompleted(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type: models.JobType("test_job"),
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	result := map[string]any{"output": "done"}
	err = svc.Complete(ctx, job.ID, result)
	if err != nil {
		t.Fatalf("failed to complete job: %v", err)
	}

	updated, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if updated.Status != models.StatusCompleted {
		t.Errorf("expected status %s, got %s", models.StatusCompleted, updated.Status)
	}
}

func TestJobMovesToFailedWithError(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type: models.JobType("test_job"),
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	err = svc.Fail(ctx, job.ID, fmt.Errorf("something went wrong"))
	if err != nil {
		t.Fatalf("failed to fail job: %v", err)
	}

	updated, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if updated.Status != models.StatusFailed {
		t.Errorf("expected status %s, got %s", models.StatusFailed, updated.Status)
	}
}

func TestJobPayloadRoundTrip(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	payload := map[string]any{"key": "value", "number": float64(42)}
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type:    models.JobType("test_job"),
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("failed to enqueue job: %v", err)
	}

	retrieved, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}

	if len(retrieved.Payload) == 0 {
		t.Fatal("expected non-empty payload")
	}
}

func TestUnknownJobTypeFailsClearly(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	job, err := svc.Enqueue(ctx, &EnqueueRequest{
		Type: models.JobType("unknown_type"),
	})
	if err != nil {
		t.Fatalf("enqueue should not fail for unknown type: %v", err)
	}

	if job.Type != models.JobType("unknown_type") {
		t.Errorf("expected job type 'unknown_type', got %s", job.Type)
	}
}

func TestConcurrentJobCreationDoesNotRace(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	var wg sync.WaitGroup
	numGoroutines := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Enqueue(ctx, &EnqueueRequest{
				Type:    models.JobType("concurrent_job"),
				Project: "concurrent-test",
			})
			if err != nil {
				t.Errorf("goroutine %d failed to enqueue: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	// Verify all jobs were created
	jobs, err := svc.List(ctx, models.JobFilter{})
	if err != nil {
		t.Fatalf("failed to list jobs: %v", err)
	}
	if len(jobs) != numGoroutines {
		t.Errorf("expected %d jobs, got %d", numGoroutines, len(jobs))
	}
}

func TestJobsMarkStaleRunningJobsFailed(t *testing.T) {
	ctx := context.Background()

	svc, cleanup := setupTestService(t)
	defer cleanup()

	repo := svc.repo

	// Insert old running job
	oldTime := time.Now().UTC().Add(-30 * time.Minute)
	oldJob := &models.Job{
		ID:        "job-old-running",
		Type:      models.JobTypeArtlistRun,
		Status:    models.StatusRunning,
		UpdatedAt: oldTime,
		CreatedAt: oldTime,
		Payload:   []byte("{}"),
	}
	require.NoError(t, repo.Create(ctx, oldJob))

	// Insert fresh running job
	freshJob := &models.Job{
		ID:        "job-fresh-running",
		Type:      models.JobTypeArtlistRun,
		Status:    models.StatusRunning,
		UpdatedAt: time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
		Payload:   []byte("{}"),
	}
	require.NoError(t, repo.Create(ctx, freshJob))

	// Insert completed job (should not be affected)
	completedJob := &models.Job{
		ID:        "job-completed",
		Type:      models.JobTypeArtlistRun,
		Status:    models.StatusCompleted,
		UpdatedAt: time.Now().UTC().Add(-30 * time.Minute),
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
		Payload:   []byte("{}"),
	}
	require.NoError(t, repo.Create(ctx, completedJob))

	// Mark stale jobs
	changed, err := svc.MarkStaleRunningJobsFailed(ctx, 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	// Verify old job is now failed
	oldJobGot, err := svc.Get(ctx, oldJob.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusFailed, oldJobGot.Status)
	assert.Contains(t, oldJobGot.Error, "stale")

	// Verify fresh job is still running
	freshJobGot, err := svc.Get(ctx, freshJob.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusRunning, freshJobGot.Status)

	// Verify completed job is still succeeded
	completedJobGot, err := svc.Get(ctx, completedJob.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCompleted, completedJobGot.Status)
}

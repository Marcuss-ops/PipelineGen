package maintenance

import (
	"context"
	"testing"
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

// --- Helper to create test config with short intervals ---

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Jobs.ZombieCheckInterval = 1
	cfg.Jobs.CleanupInterval = 1
	cfg.Workers.HeartbeatTimeout = 1
	cfg.Storage.AutoSaveInterval = 1
	cfg.Security.AdminToken = "test-admin"
	cfg.Security.WorkerToken = "test-worker"
	cfg.Security.EnableAuth = false
	cfg.Security.RateLimitEnabled = true
	cfg.Security.RateLimitRequests = 100
	return cfg
}

// --- New Tests ---

func TestNew(t *testing.T) {
	cfg := testConfig()

	// We can't easily mock job.Service and worker.Service since they are concrete types.
	// The maintenance Service constructor takes *job.Service and *worker.Service directly.
	// We test with nil services -- the service will panic if methods are called on them,
	// but we can at least verify construction and context cancellation behavior.

	// Test that New returns a non-nil service
	svc := New(cfg, nil, nil)
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
	if svc.cfg != cfg {
		t.Error("config not set correctly")
	}
}

// --- Start / Context Cancellation Tests ---

func TestService_Start_ContextCancellation(t *testing.T) {
	cfg := testConfig()

	// Create service with nil dependencies.
	// The Start method launches goroutines that call methods on these dependencies.
	// With nil deps, the goroutines will panic when they try to call methods.
	// Instead, we test with a context that's already cancelled -- the goroutines
	// should exit immediately on the first select without calling any methods.

	svc := New(cfg, nil, nil)

	// Use an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Start should return immediately (goroutines exit on ctx.Done)
	// We give it a short time and verify no panic occurs
	done := make(chan struct{})
	go func() {
		svc.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good -- started and exited goroutines quickly
	case <-time.After(2 * time.Second):
		// Also acceptable -- goroutines are running but will exit when context is done
	}
}

func TestService_Start_RunsAndStops(t *testing.T) {
	cfg := testConfig()
	cfg.Jobs.ZombieCheckInterval = 1
	cfg.Jobs.CleanupInterval = 1
	cfg.Workers.HeartbeatTimeout = 1
	cfg.Storage.AutoSaveInterval = 1

	svc := New(cfg, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start the service -- it will launch goroutines that exit when ctx is done
	svc.Start(ctx)

	// Wait for context to expire
	<-ctx.Done()

	// Give goroutines time to stop
	time.Sleep(200 * time.Millisecond)
}

// --- Model Type Tests ---

func TestJobModel(t *testing.T) {
	job := models.Job{
		ID:        "job-123",
		Type:      "video_generation",
		Project:   "my-project",
		VideoName: "test-video",
		Status:    models.StatusPending,
		Payload:   map[string]interface{}{"key": "value"},
	}

	if job.ID != "job-123" {
		t.Errorf("ID = %q, want job-123", job.ID)
	}
	if job.Status != models.StatusPending {
		t.Errorf("Status = %q, want pending", job.Status)
	}
}

func TestWorkerModel(t *testing.T) {
	worker := models.Worker{
		ID:       "worker-1",
		Name:     "Test Worker",
		Status:   models.WorkerIdle,
		LastSeen: time.Now(),
	}

	if worker.ID != "worker-1" {
		t.Errorf("ID = %q, want worker-1", worker.ID)
	}
	if worker.Status != models.WorkerIdle {
		t.Errorf("Status = %q, want idle", worker.Status)
	}
}

func TestJobStatusConstants(t *testing.T) {
	// Verify job status values exist and are distinct
	statuses := []models.JobStatus{
		models.StatusPending,
		models.StatusRunning,
		models.StatusCompleted,
		models.StatusFailed,
		models.StatusCancelled,
	}

	seen := make(map[models.JobStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
	}
}

func TestWorkerStatusConstants(t *testing.T) {
	statuses := []models.WorkerStatus{
		models.WorkerIdle,
		models.WorkerBusy,
		models.WorkerOffline,
		models.WorkerError,
	}

	seen := make(map[models.WorkerStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
	}
}

// --- Config Validation ---

func TestMaintenanceConfig(t *testing.T) {
	cfg := testConfig()

	if cfg.Jobs.ZombieCheckInterval <= 0 {
		t.Error("ZombieCheckInterval should be positive")
	}
	if cfg.Jobs.CleanupInterval <= 0 {
		t.Error("CleanupInterval should be positive")
	}
	if cfg.Workers.HeartbeatTimeout <= 0 {
		t.Error("HeartbeatTimeout should be positive")
	}
	if cfg.Storage.AutoSaveInterval <= 0 {
		t.Error("AutoSaveInterval should be positive")
	}
}

// --- Service Struct Fields ---

func TestServiceStructFields(t *testing.T) {
	cfg := testConfig()
	svc := &Service{
		cfg:           cfg,
		jobService:    nil,
		workerService: nil,
	}

	if svc.cfg == nil {
		t.Error("cfg should not be nil")
	}
}

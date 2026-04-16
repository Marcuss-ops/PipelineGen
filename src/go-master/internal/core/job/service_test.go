package job

import (
	"context"
	"errors"
	"testing"

	"velox/go-master/pkg/models"
)

// MockStorage implements StorageInterface for testing
type MockStorage struct {
	jobs       map[string]*models.Job
	events     []*models.JobEvent
	queue      *models.Queue
	shouldFail bool
}

func NewMockStorage() *MockStorage {
	return &MockStorage{
		jobs:  make(map[string]*models.Job),
		queue: &models.Queue{Jobs: []*models.Job{}},
	}
}

func (m *MockStorage) GetJob(ctx context.Context, id string) (*models.Job, error) {
	if m.shouldFail {
		return nil, ErrJobNotFound
	}
	job, ok := m.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	return job, nil
}

func (m *MockStorage) SaveJob(ctx context.Context, job *models.Job) error {
	if m.shouldFail {
		return errors.New("mock save error")
	}
	m.jobs[job.ID] = job
	return nil
}

func (m *MockStorage) DeleteJob(ctx context.Context, id string) error {
	delete(m.jobs, id)
	return nil
}

func (m *MockStorage) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	var result []*models.Job
	for _, job := range m.jobs {
		result = append(result, job)
	}
	return result, nil
}

func (m *MockStorage) LoadQueue(ctx context.Context) (*models.Queue, error) {
	return m.queue, nil
}

func (m *MockStorage) SaveQueue(ctx context.Context, queue *models.Queue) error {
	m.queue = queue
	return nil
}

func (m *MockStorage) LogJobEvent(ctx context.Context, event *models.JobEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *MockStorage) GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error) {
	var result []*models.JobEvent
	for _, e := range m.events {
		if e.JobID == jobID {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func TestNewService(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	if service == nil {
		t.Fatal("NewService returned nil")
	}
	
	if service.queue == nil {
		t.Error("queue should not be nil")
	}
}

func TestCreateJob(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:      models.JobTypeVideoGeneration,
		Project:   "test-project",
		VideoName: "test-video",
		Payload:   map[string]interface{}{"test": "data"},
	}
	
	job, err := service.CreateJob(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	
	if job.ID == "" {
		t.Error("job ID should not be empty")
	}
	
	if job.Type != models.JobTypeVideoGeneration {
		t.Errorf("expected job type %s, got %s", models.JobTypeVideoGeneration, job.Type)
	}
	
	if job.Project != "test-project" {
		t.Errorf("expected project %s, got %s", "test-project", job.Project)
	}
}

func TestGetJob(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:    models.JobTypeVoiceover,
		Project: "test-project",
	}
	
	created, _ := service.CreateJob(context.Background(), req)
	
	job, err := service.GetJob(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	
	if job.ID != created.ID {
		t.Errorf("expected job ID %s, got %s", created.ID, job.ID)
	}
}

func TestGetJobNotFound(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	_, err := service.GetJob(context.Background(), "nonexistent-id")
	if err != ErrJobNotFound {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}
}

func TestUpdateJobStatus(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:    models.JobTypeScript,
		Project: "test-project",
	}
	
	job, _ := service.CreateJob(context.Background(), req)
	
	// Proper transition: pending -> queued -> running
	err := service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusQueued, 0, nil, "")
	if err != nil {
		t.Fatalf("UpdateJobStatus to Queued failed: %v", err)
	}
	
	err = service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusRunning, 50, nil, "")
	if err != nil {
		t.Fatalf("UpdateJobStatus to Running failed: %v", err)
	}
	
	// Verify
	updated, _ := service.GetJob(context.Background(), job.ID)
	if updated.Status != models.JobStatusRunning {
		t.Errorf("expected status %s, got %s", models.JobStatusRunning, updated.Status)
	}
	
	if updated.Progress != 50 {
		t.Errorf("expected progress 50, got %d", updated.Progress)
	}
}

func TestInvalidStatusTransition(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:    models.JobTypeStockClip,
		Project: "test",
	}
	
	job, _ := service.CreateJob(context.Background(), req)
	
	// Go to running first
	service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusQueued, 0, nil, "")
	service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusRunning, 0, nil, "")
	
	// Try invalid transition from Running back to Pending
	err := service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusPending, 0, nil, "")
	if err != ErrInvalidJobStatus {
		t.Errorf("expected ErrInvalidJobStatus, got %v", err)
	}
}

func TestDeleteJob(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:    models.JobTypeUpload,
		Project: "test",
	}
	
	job, _ := service.CreateJob(context.Background(), req)
	
	err := service.DeleteJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("DeleteJob failed: %v", err)
	}
	
	_, err = service.GetJob(context.Background(), job.ID)
	if err != ErrJobNotFound {
		t.Errorf("expected ErrJobNotFound after deletion")
	}
}

func TestAssignJobToWorker(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:    models.JobTypeVideoGeneration,
		Project: "test",
	}
	
	job, _ := service.CreateJob(context.Background(), req)
	
	err := service.AssignJobToWorker(context.Background(), job.ID, "worker-1")
	if err != nil {
		t.Fatalf("AssignJobToWorker failed: %v", err)
	}
	
	updated, _ := service.GetJob(context.Background(), job.ID)
	if updated.Status != models.JobStatusRunning {
		t.Errorf("expected status Running, got %s", updated.Status)
	}
	
	if updated.WorkerID != "worker-1" {
		t.Errorf("expected worker ID worker-1, got %s", updated.WorkerID)
	}
	
	if updated.LeaseExpiry == nil {
		t.Error("lease expiry should be set")
	}
}

func TestGetNextPendingJob(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	service.CreateJob(context.Background(), models.CreateJobRequest{Type: models.JobTypeVideoGeneration, Project: "p1"})
	service.CreateJob(context.Background(), models.CreateJobRequest{Type: models.JobTypeVoiceover, Project: "p1"})
	
	job := service.GetNextPendingJob([]models.WorkerCapability{models.WorkerCapabilityVideoGen}, "w1")
	if job == nil {
		t.Fatal("expected a pending job")
	}
	
	if job.Type != models.JobTypeVideoGeneration {
		t.Errorf("expected VideoGeneration job, got %s", job.Type)
	}
}

func TestIsNewJobsPaused(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	if service.IsNewJobsPaused() {
		t.Error("new jobs should not be paused by default")
	}
	
	service.SetNewJobsPaused(true)
	if !service.IsNewJobsPaused() {
		t.Error("new jobs should be paused")
	}
	
	service.SetNewJobsPaused(false)
	if service.IsNewJobsPaused() {
		t.Error("new jobs should not be paused")
	}
}

func TestIsValidStatusTransition(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	tests := []struct {
		from  models.JobStatus
		to    models.JobStatus
		valid bool
	}{
		{models.JobStatusPending, models.JobStatusQueued, true},
		{models.JobStatusPending, models.JobStatusCancelled, true},
		{models.JobStatusQueued, models.JobStatusRunning, true},
		{models.JobStatusRunning, models.JobStatusCompleted, true},
		{models.JobStatusRunning, models.JobStatusFailed, true},
		{models.JobStatusCompleted, models.JobStatusRunning, false},
		{models.JobStatusCancelled, models.JobStatusRunning, false},
	}
	
	for _, tt := range tests {
		result := service.isValidStatusTransition(tt.from, tt.to)
		if result != tt.valid {
			t.Errorf("isValidStatusTransition(%s -> %s) = %v, want %v", tt.from, tt.to, result, tt.valid)
		}
	}
}

func TestCompleteJobFlow(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	// Create job
	req := models.CreateJobRequest{
		Type:      models.JobTypeVideoGeneration,
		Project:   "flow-test",
		VideoName: "test",
	}
	
	job, err := service.CreateJob(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	
	// Assign to worker - this sets status to Running
	err = service.AssignJobToWorker(context.Background(), job.ID, "worker-1")
	if err != nil {
		t.Fatalf("AssignJobToWorker failed: %v", err)
	}
	
	// Complete job directly (skip progress update since already Running)
	err = service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusCompleted, 100, map[string]interface{}{"output": "/path/to/video.mp4"}, "")
	if err != nil {
		t.Fatalf("UpdateJobStatus to Completed failed: %v", err)
	}
	
	// Verify final state
	completed, _ := service.GetJob(context.Background(), job.ID)
	if completed.Status != models.JobStatusCompleted {
		t.Errorf("expected Completed status, got %s", completed.Status)
	}
	if completed.Progress != 100 {
		t.Errorf("expected progress 100, got %d", completed.Progress)
	}
	if completed.Result == nil {
		t.Error("result should be set")
	}
}

func TestJobRetry(t *testing.T) {
	storage := NewMockStorage()
	service := NewService(storage, nil)
	
	req := models.CreateJobRequest{
		Type:       models.JobTypeScript,
		Project:    "retry-test",
		MaxRetries: 3,
	}
	
	job, _ := service.CreateJob(context.Background(), req)
	
	// Fail the job
	service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusQueued, 0, nil, "")
	service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusRunning, 0, nil, "")
	service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusFailed, 0, nil, "First attempt failed")
	
	// Retry - should be allowed because MaxRetries > 0
	err := service.UpdateJobStatus(context.Background(), job.ID, models.JobStatusQueued, 0, nil, "")
	if err != nil {
		t.Errorf("Retry should be allowed: %v", err)
	}
	
	updated, _ := service.GetJob(context.Background(), job.ID)
	if updated.RetryCount != 1 {
		t.Errorf("expected retry count 1, got %d", updated.RetryCount)
	}
}

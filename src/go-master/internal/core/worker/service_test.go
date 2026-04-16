package worker

import (
	"context"
	"errors"
	"testing"

	"velox/go-master/pkg/models"
)

// MockWorkerStorage implements StorageInterface for testing
type MockWorkerStorage struct {
	workers      map[string]*models.Worker
	revoked      map[string]bool
	quarantined  map[string]*QuarantineInfo
	commands     map[string]*models.WorkerCommand
	shouldFail   bool
}

func NewMockWorkerStorage() *MockWorkerStorage {
	return &MockWorkerStorage{
		workers:    make(map[string]*models.Worker),
		revoked:    make(map[string]bool),
		quarantined: make(map[string]*QuarantineInfo),
		commands:   make(map[string]*models.WorkerCommand),
	}
}

func (m *MockWorkerStorage) LoadWorkers(ctx context.Context) (map[string]*models.Worker, error) {
	if m.shouldFail {
		return nil, ErrWorkerNotFound
	}
	return m.workers, nil
}

func (m *MockWorkerStorage) SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error {
	if m.shouldFail {
		return errors.New("mock save error")
	}
	m.workers = workers
	return nil
}

func (m *MockWorkerStorage) GetWorker(ctx context.Context, id string) (*models.Worker, error) {
	worker, ok := m.workers[id]
	if !ok {
		return nil, ErrWorkerNotFound
	}
	return worker, nil
}

func (m *MockWorkerStorage) SaveWorker(ctx context.Context, worker *models.Worker) error {
	m.workers[worker.ID] = worker
	return nil
}

func (m *MockWorkerStorage) DeleteWorker(ctx context.Context, id string) error {
	delete(m.workers, id)
	return nil
}

func (m *MockWorkerStorage) LoadRevokedWorkers(ctx context.Context) (map[string]bool, error) {
	return m.revoked, nil
}

func (m *MockWorkerStorage) SaveRevokedWorkers(ctx context.Context, revoked map[string]bool) error {
	m.revoked = revoked
	return nil
}

func (m *MockWorkerStorage) LoadQuarantinedWorkers(ctx context.Context) (map[string]*QuarantineInfo, error) {
	return m.quarantined, nil
}

func (m *MockWorkerStorage) SaveQuarantinedWorkers(ctx context.Context, quarantined map[string]*QuarantineInfo) error {
	m.quarantined = quarantined
	return nil
}

func (m *MockWorkerStorage) SaveWorkerCommand(ctx context.Context, cmd *models.WorkerCommand) error {
	m.commands[cmd.ID] = cmd
	return nil
}

func (m *MockWorkerStorage) GetWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	var result []*models.WorkerCommand
	for _, cmd := range m.commands {
		if cmd.WorkerID == workerID {
			result = append(result, cmd)
		}
	}
	return result, nil
}

func (m *MockWorkerStorage) AckWorkerCommand(ctx context.Context, commandID string) error {
	if cmd, ok := m.commands[commandID]; ok {
		cmd.Acknowledged = true
	}
	return nil
}

func TestNewWorkerService(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	if service == nil {
		t.Fatal("NewService returned nil")
	}
	
	if service.workers == nil {
		t.Error("workers map should not be nil")
	}
}

func TestRegisterWorker(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	req := models.WorkerRegistrationRequest{
		Name:          "test-worker",
		Hostname:      "test-host",
		IP:            "192.168.1.100",
		Port:          8080,
		Capabilities:  []models.WorkerCapability{models.WorkerCapabilityVideoGen},
		Version:       "1.0.0",
		DiskTotalGB:   500,
		MemoryTotalMB: 8192,
	}
	
	worker, token, err := service.RegisterWorker(context.Background(), req)
	if err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}
	
	if worker.ID == "" {
		t.Error("worker ID should not be empty")
	}
	
	if token == "" {
		t.Error("token should not be empty")
	}
	
	if worker.Name != "test-worker" {
		t.Errorf("expected name test-worker, got %s", worker.Name)
	}
	
	if worker.Status != models.WorkerStatusOnline {
		t.Errorf("expected status Online, got %s", worker.Status)
	}
}

func TestGetWorker(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	req := models.WorkerRegistrationRequest{
		Name:         "get-test",
		IP:           "192.168.1.1",
		Capabilities: []models.WorkerCapability{models.WorkerCapabilityVoiceover},
	}
	
	created, _, _ := service.RegisterWorker(context.Background(), req)
	
	worker, err := service.GetWorker(created.ID)
	if err != nil {
		t.Fatalf("GetWorker failed: %v", err)
	}
	
	if worker.ID != created.ID {
		t.Errorf("expected worker ID %s, got %s", created.ID, worker.ID)
	}
}

func TestGetWorkerNotFound(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	_, err := service.GetWorker("nonexistent-id")
	if err != ErrWorkerNotFound {
		t.Errorf("expected ErrWorkerNotFound, got %v", err)
	}
}

func TestGetWorkerByToken(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	req := models.WorkerRegistrationRequest{
		Name:         "token-test",
		IP:           "192.168.1.2",
		Capabilities: []models.WorkerCapability{models.WorkerCapabilityScript},
	}
	
	created, token, _ := service.RegisterWorker(context.Background(), req)
	
	worker, err := service.GetWorkerByToken(token)
	if err != nil {
		t.Fatalf("GetWorkerByToken failed: %v", err)
	}
	
	if worker.ID != created.ID {
		t.Errorf("expected worker ID %s, got %s", created.ID, worker.ID)
	}
}

func TestListWorkers(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "worker1", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen}})
	service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "worker2", IP: "192.168.1.2", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVoiceover}})
	
	workers := service.ListWorkers()
	if len(workers) != 2 {
		t.Errorf("expected 2 workers, got %d", len(workers))
	}
}

func TestUpdateWorkerStatus(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	worker, _, _ := service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "status-test", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen}})
	
	heartbeat := &models.WorkerHeartbeat{
		WorkerID:     worker.ID,
		Status:       models.WorkerStatusBusy,
		CurrentJobID: "job-123",
		DiskFreeGB:   100,
		MemoryFreeMB: 4096,
		CPUUsage:     75,
	}
	
	err := service.UpdateWorkerStatus(context.Background(), heartbeat)
	if err != nil {
		t.Fatalf("UpdateWorkerStatus failed: %v", err)
	}
	
	updated, _ := service.GetWorker(worker.ID)
	if updated.Status != models.WorkerStatusBusy {
		t.Errorf("expected status Busy, got %s", updated.Status)
	}
	
	if updated.CurrentJobID != "job-123" {
		t.Errorf("expected job ID job-123, got %s", updated.CurrentJobID)
	}
}

func TestRevokeWorker(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	worker, _, _ := service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "revoke-test", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen}})
	
	err := service.RevokeWorker(context.Background(), worker.ID, "testing revocation")
	if err != nil {
		t.Fatalf("RevokeWorker failed: %v", err)
	}
	
	if !service.IsWorkerRevoked(worker.ID) {
		t.Error("worker should be revoked")
	}
	
	_, err = service.GetWorker(worker.ID)
	if err != ErrWorkerNotFound {
		t.Error("worker should not be found after revocation")
	}
}

func TestQuarantineWorker(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	worker, _, _ := service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "quarantine-test", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen},})
	
	err := service.QuarantineWorker(context.Background(), worker.ID, "too many errors")
	if err != nil {
		t.Fatalf("QuarantineWorker failed: %v", err)
	}
	
	if !service.IsWorkerQuarantined(worker.ID) {
		t.Error("worker should be quarantined")
	}
}

func TestUnquarantineWorker(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	worker, _, _ := service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "unquarantine-test", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen}})
	service.QuarantineWorker(context.Background(), worker.ID, "test")
	
	err := service.UnquarantineWorker(context.Background(), worker.ID)
	if err != nil {
		t.Fatalf("UnquarantineWorker failed: %v", err)
	}
	
	if service.IsWorkerQuarantined(worker.ID) {
		t.Error("worker should not be quarantined")
	}
}

func TestIsWorkerSchedulable(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	worker, _, _ := service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "schedule-test", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen}})
	
	if !service.IsWorkerSchedulable(worker) {
		t.Error("worker should be schedulable")
	}
	
	service.QuarantineWorker(context.Background(), worker.ID, "test")
	if service.IsWorkerSchedulable(worker) {
		t.Error("worker should not be schedulable when quarantined")
	}
}

func TestGetWorkerStats(t *testing.T) {
	storage := NewMockWorkerStorage()
	service := NewService(storage, nil)
	
	service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "online1", IP: "192.168.1.1", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVideoGen}})
	service.RegisterWorker(context.Background(), models.WorkerRegistrationRequest{Name: "online2", IP: "192.168.1.2", Capabilities: []models.WorkerCapability{models.WorkerCapabilityVoiceover}})
	
	stats := service.GetWorkerStats()
	
	if stats["total"].(int) != 2 {
		t.Errorf("expected 2 total workers, got %d", stats["total"])
	}
	
	if stats["online"].(int) != 2 {
		t.Errorf("expected 2 online workers, got %d", stats["online"])
	}
}

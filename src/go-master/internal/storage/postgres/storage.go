package postgres

import (
	"context"
	"errors"

	"velox/go-master/internal/core/worker"
	"velox/go-master/pkg/models"
)

var ErrPostgresBackendPending = errors.New("postgres backend wiring added but repository methods are not fully implemented yet")

// Storage is the future primary durable backend.
// This initial version provides the bootstrap boundary and interface compatibility.
type Storage struct {
	dsn string
}

func NewStorage(dsn string) (*Storage, error) {
	if dsn == "" {
		return nil, errors.New("postgres dsn is required")
	}
	return &Storage{dsn: dsn}, nil
}

func (s *Storage) LoadQueue(ctx context.Context) (*models.Queue, error) {
	return nil, ErrPostgresBackendPending
}
func (s *Storage) SaveQueue(ctx context.Context, queue *models.Queue) error {
	return ErrPostgresBackendPending
}
func (s *Storage) GetJob(ctx context.Context, id string) (*models.Job, error) {
	return nil, ErrPostgresBackendPending
}
func (s *Storage) SaveJob(ctx context.Context, job *models.Job) error {
	return ErrPostgresBackendPending
}
func (s *Storage) DeleteJob(ctx context.Context, id string) error {
	return ErrPostgresBackendPending
}
func (s *Storage) ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error) {
	return nil, ErrPostgresBackendPending
}
func (s *Storage) LogJobEvent(ctx context.Context, event *models.JobEvent) error {
	return ErrPostgresBackendPending
}
func (s *Storage) GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error) {
	return nil, ErrPostgresBackendPending
}

func (s *Storage) LoadWorkers(ctx context.Context) (map[string]*models.Worker, error) {
	return nil, ErrPostgresBackendPending
}
func (s *Storage) SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error {
	return ErrPostgresBackendPending
}
func (s *Storage) GetWorker(ctx context.Context, id string) (*models.Worker, error) {
	return nil, ErrPostgresBackendPending
}
func (s *Storage) SaveWorker(ctx context.Context, w *models.Worker) error {
	return ErrPostgresBackendPending
}
func (s *Storage) DeleteWorker(ctx context.Context, id string) error {
	return ErrPostgresBackendPending
}
func (s *Storage) SaveWorkerCommand(ctx context.Context, command *models.WorkerCommand) error {
	return ErrPostgresBackendPending
}
func (s *Storage) GetWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	return nil, ErrPostgresBackendPending
}
func (s *Storage) AckWorkerCommand(ctx context.Context, commandID string) error {
	return ErrPostgresBackendPending
}
func (s *Storage) LoadRevokedWorkers(ctx context.Context) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (s *Storage) SaveRevokedWorkers(ctx context.Context, revoked map[string]bool) error {
	return ErrPostgresBackendPending
}
func (s *Storage) LoadQuarantinedWorkers(ctx context.Context) (map[string]*worker.QuarantineInfo, error) {
	return map[string]*worker.QuarantineInfo{}, nil
}
func (s *Storage) SaveQuarantinedWorkers(ctx context.Context, quarantined map[string]*worker.QuarantineInfo) error {
	return ErrPostgresBackendPending
}

func (s *Storage) Close() error { return nil }

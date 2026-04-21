// Package worker provides worker management business logic for Agent 1.
// This file defines interfaces and types for worker management.
package worker

import (
	"context"

	"velox/go-master/pkg/models"
)

// StorageInterface defines the interface for worker storage operations.
// This interface is implemented by Agent 2 (Storage Layer).
type StorageInterface interface {
	// Worker storage operations
	LoadWorkers(ctx context.Context) (map[string]*models.Worker, error)
	SaveWorkers(ctx context.Context, workers map[string]*models.Worker) error
	GetWorker(ctx context.Context, id string) (*models.Worker, error)
	SaveWorker(ctx context.Context, worker *models.Worker) error
	DeleteWorker(ctx context.Context, id string) error

	// Worker commands
	SaveWorkerCommand(ctx context.Context, command *models.WorkerCommand) error
	GetWorkerCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error)
	AckWorkerCommand(ctx context.Context, commandID string) error

	// Quarantine/Revocation lists
	LoadRevokedWorkers(ctx context.Context) (map[string]bool, error)
	SaveRevokedWorkers(ctx context.Context, revoked map[string]bool) error
	LoadQuarantinedWorkers(ctx context.Context) (map[string]*QuarantineInfo, error)
	SaveQuarantinedWorkers(ctx context.Context, quarantined map[string]*QuarantineInfo) error
}

// QuarantineInfo holds information about a quarantined worker
type QuarantineInfo struct {
	WorkerID    string `json:"worker_id"`
	Reason      string `json:"reason"`
	QuarantinedAt int64 `json:"quarantined_at"`
	ErrorCount  int    `json:"error_count"`
}

// FailHistoryEntry tracks worker failures
type FailHistoryEntry struct {
	WorkerID  string `json:"worker_id"`
	Timestamp int64  `json:"timestamp"`
	Error     string `json:"error"`
	JobID     string `json:"job_id,omitempty"`
}
// Package job provides job management business logic for Agent 1.
// This file defines interfaces that other agents must implement.
package job

import (
	"context"

	"velox/go-master/pkg/models"
)

// StorageInterface defines the interface for job storage operations.
// This interface is implemented by Agent 2 (Storage Layer).
type StorageInterface interface {
	// Job storage operations
	LoadQueue(ctx context.Context) (*models.Queue, error)
	SaveQueue(ctx context.Context, queue *models.Queue) error
	GetJob(ctx context.Context, id string) (*models.Job, error)
	SaveJob(ctx context.Context, job *models.Job) error
	DeleteJob(ctx context.Context, id string) error
	ListJobs(ctx context.Context, filter models.JobFilter) ([]*models.Job, error)

	// Job event logging
	LogJobEvent(ctx context.Context, event *models.JobEvent) error
	GetJobEvents(ctx context.Context, jobID string, limit int) ([]*models.JobEvent, error)
}

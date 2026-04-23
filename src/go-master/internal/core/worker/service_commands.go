package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/models"
)

// GetPendingCommands returns pending commands for a worker
func (s *Service) GetPendingCommands(ctx context.Context, workerID string) ([]*models.WorkerCommand, error) {
	return s.storage.GetWorkerCommands(ctx, workerID)
}

// SendCommand sends a command to a worker
func (s *Service) SendCommand(ctx context.Context, workerID string, commandType string, payload map[string]interface{}) (*models.WorkerCommand, error) {
	worker, err := s.GetWorker(workerID)
	if err != nil {
		return nil, err
	}

	cmd := &models.WorkerCommand{
		ID:        uuid.New().String(),
		Type:      commandType,
		WorkerID:  workerID,
		Payload:   payload,
		CreatedAt: time.Now(),
	}

	if err := s.storage.SaveWorkerCommand(ctx, cmd); err != nil {
		return nil, err
	}

	logger.Info("Command sent to worker",
		zap.String("worker_id", workerID),
		zap.String("command_type", commandType),
	)

	_ = worker // Use worker for potential future validation
	return cmd, nil
}

// AckCommand acknowledges a command
func (s *Service) AckCommand(ctx context.Context, commandID string) error {
	return s.storage.AckWorkerCommand(ctx, commandID)
}

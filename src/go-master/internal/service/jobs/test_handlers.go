package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/models"
)

// RegisterTestHandlers registers test handlers for the job system
func RegisterTestHandlers(dispatcher *Dispatcher, log *zap.Logger) {
	// Test echo handler - just sleeps and echoes the message
	dispatcher.Register(models.JobType("test.echo"), func(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error) {
		var payload map[string]interface{}
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
			}
		}
		msg, _ := payload["message"].(string)
		if msg == "" {
			msg = "no message"
		}

		log.Info("test.echo job started", zap.String("job_id", job.ID), zap.String("message", msg))

		// Simulate some work with progress updates
		for i := 0; i <= 100; i += 20 {
			if tools.IsCancelled() {
				return nil, fmt.Errorf("job cancelled")
			}

			tools.Progress(i, fmt.Sprintf("Processing: %d%%", i))
			tools.Event("progress", fmt.Sprintf("Progress update: %d%%", i), map[string]any{"progress": i})

			time.Sleep(500 * time.Millisecond)
		}

		log.Info("test.echo job completed", zap.String("job_id", job.ID))

		return map[string]any{
			"message": msg,
			"echo":    true,
		}, nil
	})

	// Test slow handler - for testing cancel functionality
	dispatcher.Register(models.JobType("test.slow"), func(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error) {
		log.Info("test.slow job started", zap.String("job_id", job.ID))

		for i := 0; i <= 100; i += 10 {
			if tools.IsCancelled() {
				return nil, fmt.Errorf("job cancelled")
			}

			tools.Progress(i, fmt.Sprintf("Slow processing: %d%%", i))
			time.Sleep(1 * time.Second)
		}

		return map[string]any{
			"status": "completed slowly",
		}, nil
	})

	// Test fail handler - for testing retry functionality
	dispatcher.Register(models.JobType("test.fail"), func(ctx context.Context, job *models.Job, tools *JobTools) (map[string]any, error) {
		log.Info("test.fail job started", zap.String("job_id", job.ID))
		tools.Event("info", "This job will fail", nil)
		return nil, fmt.Errorf("intentional test failure")
	})
}

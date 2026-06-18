package realtime

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	corejobs "github.com/Marcuss-ops/PipelineGen/internal/core/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// JobServiceAdapter wraps the jobs.Service to implement the JobService interface
// needed by realtime.Service for enqueuing background asset generation jobs.
type JobServiceAdapter struct {
	svc *jobs.Service
	log *zap.Logger
}

// NewJobServiceAdapter creates a new adapter.
func NewJobServiceAdapter(svc *jobs.Service, log *zap.Logger) *JobServiceAdapter {
	return &JobServiceAdapter{svc: svc, log: log}
}

// EnqueueMediaGeneration enqueues a media.generate_missing_asset job.
func (a *JobServiceAdapter) EnqueueMediaGeneration(ctx context.Context, query string, source string) (string, error) {
	payload := corejobs.MediaGeneratePayload{
		Query:  query,
		Source: source,
	}

	payloadMap := make(map[string]any)
	data, _ := json.Marshal(payload)
	_ = json.Unmarshal(data, &payloadMap)

	job, err := a.svc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       models.JobTypeMediaGenerate,
		Payload:    payloadMap,
		Priority:   1,
		MaxRetries: 3,
	})
	if err != nil {
		return "", err
	}

	a.log.Info("enqueued media generation job",
		zap.String("job_id", job.ID),
		zap.String("query", query),
		zap.String("source", source))

	return job.ID, nil
}

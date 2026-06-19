package realtime

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
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
	j, err := a.svc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:    job.TypeMediaGenerate,
		Payload: map[string]any{"query": query, "source": source},
		Priority:   1,
		MaxRetries: 3,
	})
	if err != nil {
		return "", err
	}

	a.log.Info("enqueued media generation job",
		zap.String("job_id", j.ID),
		zap.String("query", query),
		zap.String("source", source))

	return j.ID, nil
}

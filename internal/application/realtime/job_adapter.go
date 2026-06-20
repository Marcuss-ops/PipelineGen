package realtime

import (
	"context"

	"go.uber.org/zap"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// JobServiceAdapter wraps the job.Service to implement the JobService interface
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
//
// Project/ActiveKey/VideoName are set to best-effort defaults derived from
// the available parameters (source for Project, query for ActiveKey/VideoName).
// These fields carry routing/dedup/observability metadata for the worker pool;
// callers that have explicit project context should use a method with richer
// parameters directly on the job service facade.
func (a *JobServiceAdapter) EnqueueMediaGeneration(ctx context.Context, query string, source string) (string, error) {
	j, err := a.svc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       jobs.TypeMediaGenerate,
		Payload:    map[string]any{"query": query, "source": source},
		Priority:   1,
		MaxRetries: 3,
		Project:    source,
		ActiveKey:  query,
		VideoName:  query,
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

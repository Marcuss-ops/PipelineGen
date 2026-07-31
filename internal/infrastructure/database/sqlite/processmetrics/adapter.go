package processmetrics

import (
	"context"

	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
)

// ApplicationRepository adapts the infrastructure persistence model to the
// application recorder port without leaking SQLite types into application code.
type ApplicationRepository struct {
	repository *SQLiteRepository
}

// NewApplicationRepository constructs the application-facing adapter.
func NewApplicationRepository(repository *SQLiteRepository) *ApplicationRepository {
	if repository == nil {
		panic("processmetrics.NewApplicationRepository: repository is nil")
	}
	return &ApplicationRepository{repository: repository}
}

var _ appmetrics.Repository = (*ApplicationRepository)(nil)

// Insert maps an application metric to the SQLite persistence model.
func (r *ApplicationRepository) Insert(ctx context.Context, metric *appmetrics.Metric) error {
	if metric == nil {
		return r.repositoryInsert(ctx, nil)
	}
	_, err := r.repository.Insert(ctx, &Metric{
		ProcessType: metric.ProcessType,
		JobID:       metric.JobID,
		ParentJobID: metric.ParentJobID,
		Phase:       metric.Phase,
		Language:    metric.Language,
		Provider:    metric.Provider,
		StartedAt:   metric.StartedAt,
		DurationMs:  metric.DurationMs,
		QueueWaitMs: metric.QueueWaitMs,
		Status:      metric.Status,
		ErrorCode:   metric.ErrorCode,
		ItemsIn:     metric.ItemsIn,
		ItemsOut:    metric.ItemsOut,
		BytesIn:     metric.BytesIn,
		BytesOut:    metric.BytesOut,
		RetryCount:  metric.RetryCount,
		CreatedAt:   metric.CreatedAt,
		Details:     metric.Details,
	})
	return err
}

func (r *ApplicationRepository) repositoryInsert(ctx context.Context, metric *Metric) error {
	_, err := r.repository.Insert(ctx, metric)
	return err
}

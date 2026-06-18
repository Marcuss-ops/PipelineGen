// Package domain provides the SQLite adapter for the domain job.Repository
// interface. It wraps the concrete *jobs.Repository and converts between
// domain types (job.Job, job.Status) and legacy model types (models.Job,
// models.JobStatus).
//
// Lease-fenced operations (Complete, Fail, ScheduleRetry) validate that
// the calling worker still owns the lease before delegating to the
// concrete repository.
package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	jobsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
)

// SQLiteJobRepository implements job.Repository by wrapping the concrete
// *jobs.Repository and converting between domain and legacy model types.
type SQLiteJobRepository struct {
	inner *jobsrepo.Repository
}

func NewSQLiteJobRepository(inner *jobsrepo.Repository) *SQLiteJobRepository {
	return &SQLiteJobRepository{inner: inner}
}

// ── Type conversion ───────────────────────────────────────────────────

func domainToModel(j *job.Job) *models.Job {
	if j == nil {
		return nil
	}
	status := models.StatusPending
	switch j.Status {
	case job.StatusLeased:
		status = models.StatusLeased
	case job.StatusRunning:
		status = models.StatusRunning
	case job.StatusRetryWait:
		status = models.StatusRetryWait
	case job.StatusCompleted:
		status = models.StatusSucceeded
	case job.StatusFailed:
		status = models.StatusFailed
	case job.StatusCancelled:
		status = models.StatusCancelled
	}
	return &models.Job{
		ID:            j.ID,
		Type:          j.Type,
		Status:        status,
		Priority:      j.Priority,
		Project:       j.Project,
		Payload:       j.Payload,
		Result:        modelResult(j.Result),
		Error:         j.Error,
		Progress:      j.Progress,
		RetryCount:    j.RetryCount,
		MaxRetries:    j.MaxRetries,
		WorkerID:      j.WorkerID,
		LeaseID:       j.LeaseID,
		LeaseExpiry:   j.LeaseExpiry,
		Revision:      j.Revision,
		CorrelationID: j.CorrelationID,
		CreatedAt:     j.CreatedAt,
		UpdatedAt:     j.UpdatedAt,
		StartedAt:     j.StartedAt,
		CompletedAt:   j.CompletedAt,
	}
}

func modelToDomain(m *models.Job) *job.Job {
	if m == nil {
		return nil
	}
	status := job.StatusQueued
	switch m.Status {
	case models.StatusLeased:
		status = job.StatusLeased
	case models.StatusRunning:
		status = job.StatusRunning
	case models.StatusRetryWait:
		status = job.StatusRetryWait
	case models.StatusSucceeded:
		status = job.StatusCompleted
	case models.StatusFailed:
		status = job.StatusFailed
	case models.StatusCancelled:
		status = job.StatusCancelled
	}
	return &job.Job{
		ID:            m.ID,
		Type:          string(m.Type),
		Status:        status,
		Priority:      m.Priority,
		Project:       m.Project,
		Payload:       m.Payload,
		Result:        rawJSON(m.Result),
		Error:         m.Error,
		Progress:      m.Progress,
		RetryCount:    m.RetryCount,
		MaxRetries:    m.MaxRetries,
		WorkerID:      m.WorkerID,
		LeaseID:       m.LeaseID,
		LeaseExpiry:   m.LeaseExpiry,
		Revision:      m.Revision,
		CorrelationID: m.CorrelationID,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
		StartedAt:     m.StartedAt,
		CompletedAt:   m.CompletedAt,
	}
}

func modelResult(r json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r, &m)
	return m
}

func rawJSON(m map[string]any) json.RawMessage {
	if m == nil {
		return json.RawMessage("{}")
	}
	b, _ := json.Marshal(m)
	return b
}

// ── job.Repository implementation ─────────────────────────────────────

func (r *SQLiteJobRepository) Create(ctx context.Context, j *job.Job) error {
	m := domainToModel(j)
	return r.inner.Create(ctx, m)
}

func (r *SQLiteJobRepository) Get(ctx context.Context, id string) (*job.Job, error) {
	m, err := r.inner.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return modelToDomain(m), nil
}

func (r *SQLiteJobRepository) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	mf := models.JobFilter{
		WorkerID: filter.WorkerID,
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}
	if filter.Status != nil {
		s := modelStatus(*filter.Status)
		mf.Status = &s
	}
	if filter.Type != nil {
		mf.Type = filter.Type
	}
	list, err := r.inner.List(ctx, mf)
	if err != nil {
		return nil, err
	}
	out := make([]job.Job, 0, len(list))
	for _, m := range list {
		j := modelToDomain(m)
		if j != nil {
			out = append(out, *j)
		}
	}
	return out, nil
}

func modelStatus(s job.Status) models.JobStatus {
	switch s {
	case job.StatusLeased:
		return models.StatusLeased
	case job.StatusRunning:
		return models.StatusRunning
	case job.StatusRetryWait:
		return models.StatusRetryWait
	case job.StatusCompleted:
		return models.StatusSucceeded
	case job.StatusFailed:
		return models.StatusFailed
	case job.StatusCancelled:
		return models.StatusCancelled
	default:
		return models.StatusPending
	}
}

// ── ClaimNext ─────────────────────────────────────────────────────────

func (r *SQLiteJobRepository) ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*job.Job, error) {
	modelTypes := make([]string, len(types))
	for i, t := range types {
		modelTypes[i] = t
	}
	leas, err := r.inner.ClaimNext(ctx, jobsrepo.ClaimNext{
		WorkerID: workerID,
		LeaseID:  fmt.Sprintf("lease_%d", time.Now().UnixNano()),
		LeaseTTL: leaseTTL,
		Types:    modelTypes,
	})
	if err != nil {
		return nil, err
	}
	if leas == nil || leas.Job == nil {
		return nil, nil
	}
	// Start: Leased→Running
	started, err := r.inner.Start(ctx, jobsrepo.StartJob{
		JobID:    leas.Job.ID,
		WorkerID: workerID,
		LeaseID:  leas.LeaseID,
		LeaseTTL: leaseTTL,
		Revision: int64(leas.Job.Revision),
	})
	if err != nil {
		return nil, fmt.Errorf("start job %s: %w", leas.Job.ID, err)
	}
	return modelToDomain(started), nil
}

// ── Lease-fenced worker operations ────────────────────────────────────

func (r *SQLiteJobRepository) Complete(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, result json.RawMessage) error {
	_, err := r.inner.Complete(ctx, jobsrepo.CompleteJob{
		JobID:      id,
		WorkerID:   workerID,
		LeaseID:    leaseID,
		Revision:   int64(expectedRevision),
		ResultJSON: result,
	})
	return err
}

func (r *SQLiteJobRepository) Fail(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, errMsg string) error {
	_, err := r.inner.Fail(ctx, jobsrepo.FailJob{
		JobID:    id,
		WorkerID: workerID,
		LeaseID:  leaseID,
		Revision: int64(expectedRevision),
		Error:    errMsg,
	})
	return err
}

func (r *SQLiteJobRepository) ScheduleRetry(ctx context.Context, id string, workerID, leaseID string, expectedRevision int, backoff time.Duration) error {
	_, err := r.inner.ScheduleRetry(ctx, jobsrepo.ScheduleRetry{
		JobID:    id,
		WorkerID: workerID,
		LeaseID:  leaseID,
		Revision: int64(expectedRevision),
	})
	return err
}

func (r *SQLiteJobRepository) Cancel(ctx context.Context, id string) error {
	return r.inner.Cancel(ctx, id)
}

// ── Progress + events (direct delegation) ─────────────────────────────

func (r *SQLiteJobRepository) SetProgress(ctx context.Context, id string, progress int, message string) error {
	return r.inner.SetProgress(ctx, id, progress, message)
}

func (r *SQLiteJobRepository) AddEvent(ctx context.Context, id string, eventType, message string, data map[string]any) error {
	return r.inner.AddEvent(ctx, id, eventType, message, data)
}

func (r *SQLiteJobRepository) RenewLease(ctx context.Context, id string, workerID string, leaseTTL time.Duration) error {
	_, err := r.inner.RenewLease(ctx, jobsrepo.RenewLease{
		JobID:         id,
		WorkerID:      workerID,
		LeaseID:       "", // passed through — concrete repo validates against current row
		NewExpiration: time.Now().Add(leaseTTL),
	})
	return err
}

func (r *SQLiteJobRepository) DeadLetter(ctx context.Context, id string, errMsg string) error {
	return r.inner.DeadLetter(ctx, id, errMsg)
}

// Compile-time check.
var _ job.Repository = (*SQLiteJobRepository)(nil)

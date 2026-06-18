// Package domain provides SQLite implementations of the canonical domain
// repository contracts defined in internal/core/domain/.
//
// These adapters sit in the infrastructure layer and satisfy the domain
// interfaces using the project's existing tables (jobs, media_assets, ...).
// They import *sql.DB and domain types; they never import Gin, Drive, or
// Qdrant.
package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	jobsrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// SQLiteJobRepository implements job.Repository by delegating to the
// canonical jobs.Repository. This is the single source of truth for
// job persistence; no duplicate SQL lives here.
//
// The adapter converts between domain types (job.Job, job.Status) and
// model types (models.Job, models.JobStatus) so that callers of the
// domain interface never depend on the models package.
type SQLiteJobRepository struct {
	inner *jobsrepo.Repository
}

// NewSQLiteJobRepository creates a new SQLiteJobRepository backed by the
// canonical jobs.Repository.
func NewSQLiteJobRepository(inner *jobsrepo.Repository) *SQLiteJobRepository {
	return &SQLiteJobRepository{inner: inner}
}

// ── Type conversion helpers ───────────────────────────────────────────

// domainToModel converts a domain job to a canonical models.Job.
//
// Asymmetric fields:
//   - Payload:  both json.RawMessage — copy directly
//   - Result:   models uses map[string]any, domain uses json.RawMessage
func domainToModel(j *job.Job) *models.Job {
	if j == nil {
		return nil
	}

	status := models.JobStatus(j.Status)
	if j.Status == "" {
		status = models.StatusPending
	}

	// Result: json.RawMessage (domain) → map[string]any (models).
	var resultMap map[string]any
	if len(j.Result) > 0 && string(j.Result) != "null" {
		_ = json.Unmarshal(j.Result, &resultMap)
	}

	m := &models.Job{
		ID:             j.ID,
		Type:           models.JobType(j.Type),
		Status:         status,
		Priority:       j.Priority,
		Project:        j.Project,
		ActiveKey:      j.CorrelationID,
		CorrelationID:  j.CorrelationID,
		CreatedAt:      j.CreatedAt,
		UpdatedAt:      j.UpdatedAt,
		StartedAt:      j.StartedAt,
		CompletedAt:    j.CompletedAt,
		WorkerID:       j.WorkerID,
		Payload:        j.Payload, // both json.RawMessage — copy directly
		Result:         resultMap,
		Error:          j.Error,
		RetryCount:     j.RetryCount,
		MaxRetries:     j.MaxRetries,
		Progress:       j.Progress,
	}
	return m
}

// modelToDomain converts a canonical models.Job to a domain job.Job.
func modelToDomain(m *models.Job) *job.Job {
	if m == nil {
		return nil
	}

	// Result: map[string]any (models) → json.RawMessage (domain).
	resultRaw, _ := json.Marshal(m.Result)
	if resultRaw == nil || string(resultRaw) == "null" {
		resultRaw = []byte("{}")
	}
	// Payload is already json.RawMessage in both types — copy directly.
	payloadRaw := m.Payload
	if len(payloadRaw) == 0 || string(payloadRaw) == "null" {
		payloadRaw = []byte("{}")
	}

	j := &job.Job{
		ID:             m.ID,
		Type:           string(m.Type),
		Status:         job.Status(m.Status),
		Priority:       m.Priority,
		Project:        m.Project,
		Payload:        payloadRaw,
		Result:         resultRaw,
		Error:          m.Error,
		Progress:       m.Progress,
		RetryCount:     m.RetryCount,
		MaxRetries:     m.MaxRetries,
		WorkerID:       m.WorkerID,
		CorrelationID:  m.CorrelationID,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		StartedAt:      m.StartedAt,
		CompletedAt:    m.CompletedAt,
	}
	return j
}

// ── job.Repository implementation ─────────────────────────────────────

// Create inserts a new job in PENDING state.
func (r *SQLiteJobRepository) Create(ctx context.Context, j *job.Job) error {
	m := domainToModel(j)
	return r.inner.Create(ctx, m)
}

// Get returns a job by ID, or nil if not found.
func (r *SQLiteJobRepository) Get(ctx context.Context, id string) (*job.Job, error) {
	m, err := r.inner.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("SQLiteJobRepository.Get(%s): %w", id, err)
	}
	return modelToDomain(m), nil
}

// List returns jobs matching the given filter.
func (r *SQLiteJobRepository) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	mFilter := models.JobFilter{
		WorkerID: filter.WorkerID,
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}
	if filter.Status != nil {
		s := models.JobStatus(*filter.Status)
		mFilter.Status = &s
	}
	if filter.Type != nil {
		t := models.JobType(*filter.Type)
		mFilter.Type = &t
	}

	models, err := r.inner.List(ctx, mFilter)
	if err != nil {
		return nil, fmt.Errorf("SQLiteJobRepository.List: %w", err)
	}

	out := make([]job.Job, 0, len(models))
	for _, m := range models {
		j := modelToDomain(m)
		if j != nil {
			out = append(out, *j)
		}
	}
	return out, nil
}

// Transition atomically transitions a job from one status to another.
// Routes through the new typed commands (CompleteJob, FailJob, StartJob, etc.)
// with fencing-token validation from the loaded current job.
func (r *SQLiteJobRepository) Transition(ctx context.Context, id string, from, to job.Status) error {
	return r.transitionInternal(ctx, id, from, to, nil, "")
}

// transitionInternal is the single call path for all job state transitions.
// It validates the transition, loads the current job for fencing tokens,
// and routes to the appropriate typed command on the canonical repo.
func (r *SQLiteJobRepository) transitionInternal(ctx context.Context, id string, from, to job.Status, result map[string]any, errMsg string) error {
	// Validate the transition using domain-level rules.
	if err := validateTransition(from, to); err != nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w", id, err)
	}

	// Load the current job to get fencing tokens (worker_id, lease_id, revision).
	current, err := r.inner.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w", id, err)
	}
	if current == nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): job not found", id)
	}

	// Verify the current status matches the expected from status.
	if current.Status != models.JobStatus(from) {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w (current=%s, expected=%s)",
			id, job.ErrTransitionConflict, current.Status, from)
	}

	// Dispatch to typed commands based on the target status.
	switch to {
	case job.StatusRunning, job.StatusLeased:
		// PENDING → LEASED/RUNNING: use Start (handles both PENDING and LEASED as from-states).
		_, err = r.inner.Start(ctx, jobsrepo.StartJob{
			JobID:    id,
			WorkerID: current.WorkerID,
			LeaseID:  current.LeaseID,
			LeaseTTL: 5 * time.Minute, // default; caller should use ClaimNext for proper lease
			Revision: int64(current.Revision),
		})
		return err

	case job.StatusSucceeded: // covers job.StatusCompleted (alias)
		var resultJSON json.RawMessage
		if result != nil {
			b, _ := json.Marshal(result)
			if b != nil {
				resultJSON = b
			}
		}
		if resultJSON == nil {
			resultJSON = json.RawMessage("{}")
		}
		_, err = r.inner.Complete(ctx, jobsrepo.CompleteJob{
			JobID:      id,
			WorkerID:   current.WorkerID,
			LeaseID:    current.LeaseID,
			Revision:   int64(current.Revision),
			ResultJSON: resultJSON,
		})
		return err

	case job.StatusFailed:
		_, err = r.inner.Fail(ctx, jobsrepo.FailJob{
			JobID:    id,
			WorkerID: current.WorkerID,
			LeaseID:  current.LeaseID,
			Revision: int64(current.Revision),
			Error:    errMsg,
		})
		return err

	case job.StatusPending: // covers job.StatusQueued (alias)
		// Retry path: FAILED → PENDING (or RETRY_WAIT → PENDING).
		// Uses the canonical Retry which handles both.
		_, err = r.inner.Retry(ctx, id)
		return err

	case job.StatusCancelled:
		return r.inner.Cancel(ctx, id)

	default:
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): unhandled target status %q", id, to)
	}
}

// ClaimNext claims the oldest PENDING job for the given worker,
// transitioning it PENDING→LEASED→RUNNING in one logical operation.
func (r *SQLiteJobRepository) ClaimNext(ctx context.Context, workerID string, leaseTTLSeconds int, types []string) (*job.Job, error) {
	jobTypes := make([]models.JobType, 0, len(types))
	for _, t := range types {
		jobTypes = append(jobTypes, models.JobType(t))
	}

	leaseID := fmt.Sprintf("lease_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))
	leaseTTL := time.Duration(leaseTTLSeconds) * time.Second

	// Step 1: ClaimNext (PENDING → LEASED with fencing token)
	leas, err := r.inner.ClaimNext(ctx, jobsrepo.ClaimNext{
		WorkerID: workerID,
		LeaseID:  leaseID,
		LeaseTTL: leaseTTL,
		Types:    jobTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("SQLiteJobRepository.ClaimNext: %w", err)
	}
	if leas == nil || leas.Job == nil {
		return nil, nil
	}

	// Step 2: Start (LEASED → RUNNING)
	started, err := r.inner.Start(ctx, jobsrepo.StartJob{
		JobID:    leas.Job.ID,
		WorkerID: workerID,
		LeaseID:  leaseID,
		LeaseTTL: leaseTTL,
		Revision: int64(leas.Job.Revision),
	})
	if err != nil {
		return nil, fmt.Errorf("SQLiteJobRepository.ClaimNext start: %w", err)
	}

	return modelToDomain(started), nil
}

// Complete marks a running job as completed with a result.
func (r *SQLiteJobRepository) Complete(ctx context.Context, id string, result json.RawMessage) error {
	var resultMap map[string]any
	if len(result) > 0 && string(result) != "null" {
		_ = json.Unmarshal(result, &resultMap)
	}
	return r.transitionInternal(ctx, id, job.StatusRunning, job.StatusCompleted, resultMap, "")
}

// Fail marks a running job as failed with an error message.
func (r *SQLiteJobRepository) Fail(ctx context.Context, id string, errMsg string) error {
	return r.transitionInternal(ctx, id, job.StatusRunning, job.StatusFailed, nil, errMsg)
}

// Cancel cancels a PENDING, LEASED, RUNNING, or RETRY_WAIT job.
func (r *SQLiteJobRepository) Cancel(ctx context.Context, id string) error {
	return r.inner.Cancel(ctx, id)
}

// Retry re-enqueues a failed job for retry.
func (r *SQLiteJobRepository) Retry(ctx context.Context, id string) error {
	current, err := r.inner.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("SQLiteJobRepository.Retry get: %w", err)
	}
	if current == nil {
		return fmt.Errorf("SQLiteJobRepository.Retry: job %s not found", id)
	}
	if current.RetryCount >= current.MaxRetries {
		return fmt.Errorf("SQLiteJobRepository.Retry: job %s exhausted retries (%d/%d)", id, current.RetryCount, current.MaxRetries)
	}

	// Use the canonical Retry which handles FAILED/RetryWait → PENDING.
	_, err = r.inner.Retry(ctx, id)
	if err != nil {
		return fmt.Errorf("SQLiteJobRepository.Retry: %w", err)
	}
	return nil
}

// ── Transition validation ─────────────────────────────────────────────

// validateTransition checks if the transition is valid per the canonical
// state machine. This operates on domain job.Status to avoid importing
// models from the domain layer.
func validateTransition(current, next job.Status) error {
	switch current {
	case job.StatusPending: // covers job.StatusQueued (alias)
		switch next {
		case job.StatusLeased, job.StatusCancelled:
			return nil
		}
	case job.StatusLeased:
		switch next {
		case job.StatusRunning, job.StatusPending, job.StatusCancelled:
			return nil
		}
	case job.StatusRunning:
		switch next {
		case job.StatusSucceeded, job.StatusFailed, job.StatusCancelled, job.StatusRetryWait:
			return nil
		}
	case job.StatusRetryWait:
		switch next {
		case job.StatusPending, job.StatusFailed, job.StatusCancelled:
			return nil
		}
	case job.StatusFailed:
		if next == job.StatusPending {
			return nil
		}
	case job.StatusSucceeded, job.StatusCancelled:
		return fmt.Errorf("cannot transition from terminal status %q to %q", current, next)
	default:
		return fmt.Errorf("unknown status %q", current)
	}
	return fmt.Errorf("invalid transition: %q → %q", current, next)
}

// Compile-time check that SQLiteJobRepository satisfies job.Repository.
var _ job.Repository = (*SQLiteJobRepository)(nil)

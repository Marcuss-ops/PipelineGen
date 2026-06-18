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
		status = models.StatusQueued
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
		WorkflowID:     j.WorkflowID,
		WorkflowStepID: j.WorkflowStepID,
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
		WorkflowID:     m.WorkflowID,
		WorkflowStepID: m.WorkflowStepID,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		StartedAt:      m.StartedAt,
		CompletedAt:    m.CompletedAt,
	}
	return j
}

// domainToModelStatus converts domain.Status to models.JobStatus.
func domainToModelStatus(s job.Status) models.JobStatus {
	return models.JobStatus(s)
}

// ── job.Repository implementation ─────────────────────────────────────

// Create inserts a new job in queued state.
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
// This is the SINGLE entry point for all job state transitions; Complete,
// Fail, and Retry all route through Transition.
//
// Delegates to the canonical repo's Transition method with the expected
// revision fetched from the current job row for optimistic concurrency.
func (r *SQLiteJobRepository) Transition(ctx context.Context, id string, from, to job.Status) error {
	return r.transitionInternal(ctx, id, from, to, nil, "")
}

// transitionInternal is the single call path for all job state transitions.
// It validates the transition, loads the current job for revision-based
// optimistic concurrency, builds the TransitionRequest, and delegates to
// the canonical repo's Transition method.
//
// When result is non-nil, it is set on the completed job.
// When errMsg is non-empty, it is set on the failed job.
func (r *SQLiteJobRepository) transitionInternal(ctx context.Context, id string, from, to job.Status, result map[string]any, errMsg string) error {
	// Validate the transition using domain-level rules.
	if err := validateTransition(from, to); err != nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w", id, err)
	}

	// Load the current job to get revision and current status.
	current, err := r.inner.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w", id, err)
	}
	if current == nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): job not found", id)
	}

	// Verify the current status matches the expected from status.
	if current.Status != domainToModelStatus(from) {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w (current=%s, expected=%s)",
			id, job.ErrTransitionConflict, current.Status, from)
	}

	req := jobsrepo.TransitionRequest{
		JobID:            id,
		ExpectedStatus:   domainToModelStatus(from),
		ExpectedRevision: current.Revision,
		NewStatus:        domainToModelStatus(to),
		// Preserve fencing tokens from the loaded job so the canonical
		// Transition enforces worker_id + lease_id ownership (prevents
		// stale-lease completion of a reassigned job).
		WorkerID: current.WorkerID,
		LeaseID:  current.LeaseID,
	}

	// Set status-specific fields that the canonical Transition doesn't handle natively.
	switch to {
	case job.StatusCompleted:
		req.Progress = intPtr(100)
		if result != nil {
			req.Result = result
		}
	case job.StatusFailed:
		if errMsg != "" {
			req.Error = &errMsg
		}
	case job.StatusQueued:
		// Retry path: clear lease, worker, and timestamps.
		req.ExtraSets = []string{
			"retry_count = retry_count + 1",
			"worker_id = ''",
			"lease_id = ''",
			"lease_expiry = NULL",
			"started_at = NULL",
			"completed_at = NULL",
			"cancelled_at = NULL",
		}
	}

	_, err = r.inner.Transition(ctx, req)
	if err != nil {
		return fmt.Errorf("SQLiteJobRepository.Transition(%s): %w", id, err)
	}
	return nil
}

// ClaimNext claims the oldest queued job for the given worker.
// Delegates to the canonical repo's CTE-based atomic claim — no claimMu needed.
func (r *SQLiteJobRepository) ClaimNext(ctx context.Context, workerID string, leaseTTLSeconds int, types []string) (*job.Job, error) {
	jobTypes := make([]models.JobType, 0, len(types))
	for _, t := range types {
		jobTypes = append(jobTypes, models.JobType(t))
	}

	m, err := r.inner.ClaimNext(ctx, workerID, time.Duration(leaseTTLSeconds)*time.Second, jobTypes)
	if err != nil {
		return nil, fmt.Errorf("SQLiteJobRepository.ClaimNext: %w", err)
	}
	return modelToDomain(m), nil
}

// Complete marks a running job as completed with a result.
// Routes through Transition (running → completed) — the single entry point
// for all job state changes.
func (r *SQLiteJobRepository) Complete(ctx context.Context, id string, result json.RawMessage) error {
	var resultMap map[string]any
	if len(result) > 0 && string(result) != "null" {
		_ = json.Unmarshal(result, &resultMap)
	}
	return r.transitionInternal(ctx, id, job.StatusRunning, job.StatusCompleted, resultMap, "")
}

// Fail marks a running job as failed with an error message.
// Routes through Transition (running → failed) — the single entry point
// for all job state changes.
func (r *SQLiteJobRepository) Fail(ctx context.Context, id string, errMsg string) error {
	return r.transitionInternal(ctx, id, job.StatusRunning, job.StatusFailed, nil, errMsg)
}

// Cancel cancels a queued or running job.
func (r *SQLiteJobRepository) Cancel(ctx context.Context, id string) error {
	return r.inner.Cancel(ctx, id)
}

// Retry re-enqueues a failed job for retry.
// Uses the adapter's Transition (failed → queued) rather than the canonical
// ScheduleRetry, which is designed for in-flight retries (running → queued)
// and requires fencing tokens that a failed job no longer holds.
func (r *SQLiteJobRepository) Retry(ctx context.Context, id string) error {
	current, err := r.inner.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("SQLiteJobRepository.Retry get: %w", err)
	}
	if current == nil {
		return fmt.Errorf("SQLiteJobRepository.Retry: job %s not found", id)
	}
	if current.Status != models.StatusFailed {
		return fmt.Errorf("SQLiteJobRepository.Retry: job %s is not failed (status=%s)", id, current.Status)
	}
	if current.RetryCount >= current.MaxRetries {
		return fmt.Errorf("SQLiteJobRepository.Retry: job %s exhausted retries (%d/%d)", id, current.RetryCount, current.MaxRetries)
	}

	// Use the adapter's Transition which handles the ExtraSets for
	// clearing worker_id, lease_id, timestamps, and incrementing retry_count.
	return r.Transition(ctx, id, job.StatusFailed, job.StatusQueued)
}

// ── Transition validation ─────────────────────────────────────────────

// validateTransition checks if the transition is valid per the canonical
// state machine (same rules as models.TransitionJob). This operates on
// domain job.Status to avoid importing models from the domain layer.
//
// Rule: jobs must go through running to reach terminal states.
// Only the worker can complete or fail a job.
//
// Allowed transitions:
//
//	queued    → running, cancelled
//	running   → completed, failed, cancelled, queued (lease expiry / retry)
//	failed    → queued (retry)
//	completed → (terminal)
//	cancelled → (terminal)
func validateTransition(current, next job.Status) error {
	switch current {
	case job.StatusQueued:
		switch next {
		case job.StatusRunning, job.StatusCancelled:
			return nil
		}
	case job.StatusRunning:
		switch next {
		case job.StatusCompleted, job.StatusFailed, job.StatusCancelled, job.StatusQueued:
			return nil
		}
	case job.StatusFailed:
		if next == job.StatusQueued {
			return nil
		}
	case job.StatusCompleted, job.StatusCancelled:
		return fmt.Errorf("cannot transition from terminal status %q to %q", current, next)
	}
	return fmt.Errorf("invalid transition: %q → %q", current, next)
}

func intPtr(i int) *int {
	return &i
}

// Compile-time check that SQLiteJobRepository satisfies job.Repository.
var _ job.Repository = (*SQLiteJobRepository)(nil)

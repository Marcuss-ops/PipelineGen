// Package reconciliation repairs stale script-generation run projections by
// comparing their durable correlation with the authoritative jobs broker.
package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type JobReader interface {
	Get(context.Context, string) (*job.Job, error)
	List(context.Context, job.Filter) ([]job.Job, error)
}

type Reconciler struct {
	Runs  scriptgen.RunReconciliationRepository
	Jobs  JobReader
	Grace time.Duration
	Limit int
}

type Report struct {
	Inspected int
	Linked    int
	Completed int
	Failed    int
	Ambiguous int
	Deferred  int
}

func (r *Reconciler) Reconcile(ctx context.Context, now time.Time) (Report, error) {
	if r == nil || r.Runs == nil || r.Jobs == nil {
		return Report{}, errors.New("script run reconciliation: run store and job reader are required")
	}
	grace := r.Grace
	if grace <= 0 {
		grace = 15 * time.Minute
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 100
	}
	candidates, err := r.Runs.ListStaleSubmissionRuns(ctx, now.UTC().Add(-grace), limit)
	if err != nil {
		return Report{}, fmt.Errorf("script run reconciliation: list candidates: %w", err)
	}
	report := Report{Inspected: len(candidates)}
	var reconcileErr error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(reconcileErr, err)
		}
		if candidate.IdempotencyKey == "" && (candidate.JobID == "" || candidate.JobID == candidate.RunID) {
			if err := r.failUnmatched(ctx, candidate, "submission has no idempotency key"); err != nil {
				reconcileErr = errors.Join(reconcileErr, fmt.Errorf("run %s: fail orphan: %w", candidate.RunID, err))
				continue
			}
			report.Failed++
			continue
		}
		found, wasUnlinked, findErr := r.findJob(ctx, candidate)
		if findErr != nil {
			if errors.Is(findErr, errAmbiguousCorrelation) {
				report.Ambiguous++
			}
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("run %s: %w", candidate.RunID, findErr))
			continue
		}
		if found == nil {
			if err := r.failUnmatched(ctx, candidate, "no script.generate job was committed for the idempotency key"); err != nil {
				reconcileErr = errors.Join(reconcileErr, fmt.Errorf("run %s: fail orphan: %w", candidate.RunID, err))
				continue
			}
			report.Failed++
			continue
		}
		if wasUnlinked {
			report.Linked++
		}
		// Also refresh the stale timestamp for a linked nonterminal job. This
		// bounds broker reads to the grace interval while keeping terminal repair
		// retryable if a later run-ledger write fails.
		if err := r.Runs.SetJobID(ctx, candidate.RunID, found.ID); err != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("run %s: persist job %s correlation: %w", candidate.RunID, found.ID, err))
			continue
		}
		if err := r.reconcileJobState(ctx, candidate, found, &report); err != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("run %s/job %s: reconcile terminal state: %w", candidate.RunID, found.ID, err))
		}
	}
	return report, reconcileErr
}

var errAmbiguousCorrelation = errors.New("idempotency key resolves to multiple jobs")

func (r *Reconciler) findJob(ctx context.Context, candidate scriptgen.StaleSubmissionRun) (*job.Job, bool, error) {
	if candidate.IdempotencyKey == "" {
		return nil, false, errors.New("run has no idempotency key")
	}
	if candidate.JobID != "" && candidate.JobID != candidate.RunID {
		found, err := r.Jobs.Get(ctx, candidate.JobID)
		if err != nil {
			return nil, false, fmt.Errorf("read linked job: %w", err)
		}
		if found == nil {
			return nil, false, errors.New("linked job is absent from the authoritative broker")
		}
		if err := validateCorrelation(candidate, found); err != nil {
			return nil, false, err
		}
		return found, false, nil
	}

	jobType, key := job.TypeScriptGenerate, candidate.IdempotencyKey
	matches, err := r.Jobs.List(ctx, job.Filter{Type: &jobType, CorrelationID: &key, Limit: 2})
	if err != nil {
		return nil, true, fmt.Errorf("find correlated job: %w", err)
	}
	switch len(matches) {
	case 0:
		return nil, true, nil
	case 1:
		if err := validateCorrelation(candidate, &matches[0]); err != nil {
			return nil, true, err
		}
		return &matches[0], true, nil
	default:
		return nil, true, errAmbiguousCorrelation
	}
}

func validateCorrelation(candidate scriptgen.StaleSubmissionRun, found *job.Job) error {
	if found == nil || found.ID == "" || found.Type != job.TypeScriptGenerate || found.CorrelationID != candidate.IdempotencyKey {
		return errors.New("broker returned a job with incomplete or mismatched run correlation")
	}
	return nil
}

func (r *Reconciler) reconcileJobState(ctx context.Context, candidate scriptgen.StaleSubmissionRun, found *job.Job, report *Report) error {
	switch found.Status {
	case job.StatusSucceeded:
		run, err := r.Runs.Get(ctx, candidate.RunID)
		if err != nil {
			return fmt.Errorf("read durable run checkpoint: %w", err)
		}
		if run == nil {
			return errors.New("run checkpoint disappeared before terminal projection")
		}
		if run.Result == nil {
			return errors.New("broker job succeeded but the durable run result checkpoint is missing; broker result is only the artifact manifest")
		}
		if err := r.Runs.UpdateStage(ctx, candidate.RunID, scriptgen.RunStatusCompleted, scriptgen.StageCompleted); err != nil {
			return fmt.Errorf("mark run completed: %w", err)
		}
		report.Completed++
	case job.StatusFailed, job.StatusCancelled, job.StatusPartiallySucceeded:
		stage := candidate.CurrentStage
		if stage == "" {
			stage = scriptgen.StageNormalizing
		}
		message := found.Error
		if message == "" {
			message = "authoritative job ended with status " + string(found.Status)
		}
		if err := r.Runs.FailRun(ctx, scriptgen.FailRunInput{
			RunID: candidate.RunID, FailedStage: stage,
			ErrorCode: "JOB_" + string(found.Status), ErrorMessage: message,
		}); err != nil {
			return fmt.Errorf("mark run failed: %w", err)
		}
		report.Failed++
	default:
		report.Deferred++
	}
	return nil
}

func (r *Reconciler) failUnmatched(ctx context.Context, run scriptgen.StaleSubmissionRun, reason string) error {
	stage := run.CurrentStage
	if stage == "" {
		stage = scriptgen.StageNormalizing
	}
	return r.Runs.FailRun(ctx, scriptgen.FailRunInput{
		RunID:        run.RunID,
		FailedStage:  stage,
		ErrorCode:    "SUBMISSION_ORPHANED",
		ErrorMessage: "stale generation run could not be correlated with its submitted job: " + reason,
	})
}

// Package jobs — job_commands.go: write-side job mutation surface.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behavior changes.
package queue

import (
	"context"
	"encoding/json"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// aggregateFlipper is the narrow Pattern-0 port the parent aggregator's
// FinalizeAggregateParent relies on. The canonical *SQLiteStore satisfies it (the
// audit 2026-07-03 P0 #1 closure added this method to its lifecycle.go).
// Future broker adapters must implement it; the type-assertion probe
// in Service.FinalizeAggregateParent fail-closes non-conformant brokers with a
// typed error rather than panicking at first aggregator tick.
//
// FASE 2 (July 2026): expectedVersion added for version-based CAS.
type aggregateFlipper interface {
	FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result []byte, errMsg string, expectedVersion int) error
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	return s.repo.Cancel(ctx, id)
}

func (s *Service) Retry(ctx context.Context, id string) (*job.Job, error) {
	return s.repo.Retry(ctx, id)
}

func (s *Service) Progress(ctx context.Context, id string, progress int, message string) error {
	return s.repo.SetProgress(ctx, id, progress, message)
}

func (s *Service) AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error {
	return s.repo.AddEvent(ctx, jobID, eventType, message, data)
}

// Complete marks a job as completed.
func (s *Service) Complete(ctx context.Context, id string, result map[string]any) error {
	resultJSON, _ := json.Marshal(result)
	return s.repo.Complete(ctx, id, "", "", 0, resultJSON)
}

// Fail marks a job as failed.
func (s *Service) Fail(ctx context.Context, id string, err error) error {
	return s.repo.Fail(ctx, id, "", "", 0, err.Error())
}

// FinalizeAggregateParent applies the canonical post-fan-out parent state flip.
//
// FASE 2 (July 2026): expectedVersion added. When > 0, the SQL layer
// adds `AND revision = expectedVersion` as a second CAS fence alongside
// the existing (status, parent_state) guard. A zero expectedVersion
// means "skip the revision check" (backward-compatible).
//
// godlike/06 SSOT: this method is the SINGLE app-layer entry point that
// transitions a parent voiceover.generate job to its final terminal
// posture. No other code path may write jobs.status or
// jobs.result.parent_state after the worker has emitted JOB_COMPLETED.
func (s *Service) FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error {
	if s == nil {
		return fmt.Errorf("jobs: FinalizeAggregateParent: nil receiver (composition bug)")
	}
	if s.repo == nil {
		return fmt.Errorf("jobs: FinalizeAggregateParent: repo not wired")
	}
	if id == "" {
		return fmt.Errorf("jobs: FinalizeAggregateParent: id is empty")
	}
	if targetStatus != job.StatusSucceeded && targetStatus != job.StatusFailed {
		return fmt.Errorf("jobs: FinalizeAggregateParent: targetStatus must be SUCCEEDED or FAILED, got %q", targetStatus)
	}
	flipper, ok := s.repo.(aggregateFlipper)
	if !ok {
		return fmt.Errorf("jobs: FinalizeAggregateParent: underlying broker %T does not implement aggregate fliper — audit 2026-07-03 migration required", s.repo)
	}
	resultJSON, _ := json.Marshal(result)
	return flipper.FinalizeAggregateParent(ctx, id, targetStatus, resultJSON, errMsg, expectedVersion)
}

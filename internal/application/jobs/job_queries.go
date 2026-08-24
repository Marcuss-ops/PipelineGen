// Package jobs — job_queries.go: read-side job query surface.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behavior changes.
//
// Forward-pointer: GetStats returns *sqljobs.JobStats (infrastructure
// type leaked to application callers). A follow-up PR will introduce a
// domain-level JobStats DTO per the decomposition plan's SQL-leakage
// removal item.
package jobs

import (
	"context"
	"fmt"

	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type requeueExpiredLeaser interface {
	RequeueExpiredLeasesNoArg(context.Context) error
}

type statsProvider interface {
	GetStats(context.Context) (*sqljobs.JobStats, error)
}

// awaitingAggregationLister is the narrow Pattern-0 port the parent
// aggregator's ListAwaitingAggregation relies on. The canonical
// *sqljobs.SQLiteStore satisfies it (migration 127 + repository.go).
//
// Commit 3 P0 #4 (July 2026): parentType parameter added so script
// and voiceover aggregators can use the same query.
type awaitingAggregationLister interface {
	ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error)
}

func (s *Service) Get(ctx context.Context, id string) (*job.Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) FindActiveByKey(ctx context.Context, activeKey string) (*job.Job, error) {
	return s.repo.FindActiveByKey(ctx, activeKey)
}

func (s *Service) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	return s.repo.List(ctx, filter)
}

// ListAwaitingAggregation returns parent jobs awaiting aggregation
// (parent_state = waiting_children, broker status IN RUNNING/
// FINALIZING/SUCCEEDED) filtered by parentType.
//
// Commit 3 P0 #4: parentType parameter scopes the query so script
// and voiceover aggregators don't cross-pollinate. Only waiting_children
// is queried — partial_success is terminal (P0 #7).
//
// Delegates to the repository's optimized query via type-assertion.
// When limit <= 0, defaults to 100.
func (s *Service) ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error) {
	if s == nil {
		return nil, fmt.Errorf("jobs: ListAwaitingAggregation: nil receiver (composition bug)")
	}
	if s.repo == nil {
		return nil, fmt.Errorf("jobs: ListAwaitingAggregation: repo not wired")
	}
	lister, ok := s.repo.(awaitingAggregationLister)
	if !ok {
		return nil, fmt.Errorf("jobs: ListAwaitingAggregation: underlying broker %T does not implement awaiting-aggregation lister — migration 127 required", s.repo)
	}
	return lister.ListAwaitingAggregation(ctx, parentType, limit)
}

// ListEvents returns the timeline events for a given job.
// Implements job.Service interface.
func (s *Service) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	return s.repo.ListEvents(ctx, jobID)
}

// IsTerminal reports whether the job status is a terminal state.
// Implements job.Service interface.
func (s *Service) IsTerminal(status job.Status) bool {
	return status.IsTerminal()
}

func (s *Service) RequeueExpiredLeases(ctx context.Context) error {
	provider, ok := s.repo.(requeueExpiredLeaser)
	if !ok {
		return fmt.Errorf("requeue expired leases: repository does not support RequeueExpiredLeasesNoArg")
	}
	return provider.RequeueExpiredLeasesNoArg(ctx)
}

// GetStats returns aggregated job statistics.
func (s *Service) GetStats(ctx context.Context) (*sqljobs.JobStats, error) {
	provider, ok := s.repo.(statsProvider)
	if !ok {
		return nil, fmt.Errorf("get stats: repository does not support GetStats")
	}
	return provider.GetStats(ctx)
}

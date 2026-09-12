package cliprender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// AggregatorJobsService is the minimum durable job surface needed to turn a
// submitted parent back into a truthful terminal job after its settle child.
type AggregatorJobsService interface {
	Get(context.Context, string) (*job.Job, error)
	ListAwaitingAggregation(context.Context, string, int) ([]job.Job, error)
	FinalizeAggregateParent(context.Context, string, job.Status, map[string]any, string, int) error
}

// DefaultParentAggregationInterval is the clip.render parent RECOVERY cadence.
//
// The healthy path is event-driven: the settle child's terminal commit reports
// its parent to FinalizeParent, so the parent flips in the same instant the
// clip finishes. This tick exists only for the case the event cannot cover — a
// process that died between committing the child and notifying, or a child
// completed by a worker that has no notifier wired. It is therefore a recovery
// sweeper, not the completion mechanism, and its interval is no longer a
// bound on a healthy clip's reported latency.
//
// 30s matches the voiceover/script aggregators: their children are
// long-running, and a stranded parent there is equally a crash-recovery case.
const DefaultParentAggregationInterval = 30 * time.Second

// ParentAggregator finalizes clip.render parents whose single settle child is
// terminal. It is intentionally small and durable: every lookup is recovered
// from the jobs table, so a restart cannot strand a submitted render.
type ParentAggregator struct {
	jobs     AggregatorJobsService
	log      *zap.Logger
	interval time.Duration
}

func NewParentAggregator(jobs AggregatorJobsService, log *zap.Logger, interval time.Duration) *ParentAggregator {
	if log == nil {
		log = zap.NewNop()
	}
	if interval <= 0 {
		interval = DefaultParentAggregationInterval
	}
	return &ParentAggregator{jobs: jobs, log: log, interval: interval}
}

func (a *ParentAggregator) Start(ctx context.Context) {
	if a == nil || a.jobs == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		a.Tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.Tick(ctx)
			}
		}
	}()
}

func (a *ParentAggregator) Tick(ctx context.Context) {
	if a == nil || a.jobs == nil {
		return
	}
	parents, err := a.jobs.ListAwaitingAggregation(ctx, TypeClipRender, 100)
	if err != nil {
		a.log.Warn("clip render parent aggregation list failed", zap.Error(err))
		return
	}
	for i := range parents {
		if err := a.aggregateOne(ctx, &parents[i]); err != nil {
			a.log.Warn("clip render parent aggregation failed", zap.String("parent_job_id", parents[i].ID), zap.Error(err))
		}
	}
}

// FinalizeParent finalises ONE clip.render parent whose settle child is already
// terminal. It is the event-driven counterpart of Tick: the worker calls it the
// instant a settle child commits, so the parent turns terminal without waiting
// for the recovery sweep.
//
// It is deliberately idempotent and cheap to call speculatively: a parent that
// is not a clip.render job, is already terminal, or still has a running child is
// a silent no-op (the finalisation itself is the same no-lease CAS Tick uses, so
// a duplicate call or a race with an in-flight Tick cannot double-flip).
func (a *ParentAggregator) FinalizeParent(ctx context.Context, parentJobID string) error {
	if a == nil || a.jobs == nil || parentJobID == "" {
		return nil
	}
	parent, err := a.jobs.Get(ctx, parentJobID)
	if err != nil {
		return fmt.Errorf("load clip render parent %s: %w", parentJobID, err)
	}
	if parent == nil {
		return nil
	}
	// Other capabilities own their own aggregators; a terminal child of a
	// voiceover/script parent must not be finalised through this path, whose
	// aggregate semantics (a single settle child) are clip.render-specific.
	if parent.Type != TypeClipRender {
		return nil
	}
	if err := a.aggregateOne(ctx, parent); err != nil {
		return err
	}
	a.log.Debug("clip render parent finalized on child completion",
		zap.String("parent_job_id", parentJobID))
	return nil
}

func (a *ParentAggregator) aggregateOne(ctx context.Context, parent *job.Job) error {
	if parent == nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(parent.Result, &result); err != nil {
		return fmt.Errorf("decode parent result: %w", err)
	}
	childID, _ := result["child_job_id"].(string)
	if childID == "" {
		return fmt.Errorf("parent result has no settle child_job_id")
	}
	child, err := a.jobs.Get(ctx, childID)
	if err != nil {
		return fmt.Errorf("load settle child %s: %w", childID, err)
	}
	if child == nil || !child.IsTerminal() {
		return nil
	}
	result["child_status"] = child.Status
	result["parent_state"] = "completed"
	result["settle_child_id"] = childID
	target := job.StatusSucceeded
	errMsg := ""
	if child.Status != job.StatusSucceeded {
		target = job.StatusFailed
		errMsg = "clip.render aggregate: settle child failed"
		result["parent_state"] = "failed"
		if child.Error != "" {
			errMsg += ": " + child.Error
		}
	}
	if err := a.jobs.FinalizeAggregateParent(ctx, parent.ID, target, result, errMsg, parent.Revision); err != nil {
		if errors.Is(err, domainremote.ErrAlreadyTerminalAggregate) {
			return nil
		}
		return err
	}
	return nil
}

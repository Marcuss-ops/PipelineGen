package localization

import (
	"context"
	"fmt"
	"sync"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"golang.org/x/sync/errgroup"
)

type RenderFunc func(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error)

type LocalizedClipTask struct {
	Priority int
	Plan     LocalizedClipPlan
}

type TaskResult struct {
	Priority int
	Artifact LocalizedClipArtifact
	Err      error
}

type Scheduler struct {
	render      RenderFunc
	g           *errgroup.Group
	ctx         context.Context
	concurrency int
	mu          sync.Mutex
	results     []TaskResult
}

func NewScheduler(ctx context.Context, render RenderFunc, concurrency int) (*Scheduler, error) {
	if render == nil {
		return nil, fmt.Errorf("localization.NewScheduler: render func is required")
	}
	if concurrency < 1 {
		concurrency = 1
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	return &Scheduler{render: render, g: g, ctx: gctx, concurrency: concurrency}, nil
}

func (s *Scheduler) Concurrency() int {
	if s == nil {
		return 0
	}
	return s.concurrency
}

func (s *Scheduler) Submit(task LocalizedClipTask) int {
	s.mu.Lock()
	idx := len(s.results)
	s.results = append(s.results, TaskResult{Priority: task.Priority})
	s.mu.Unlock()

	s.g.Go(func() error {
		// errgroup.SetLimit is the scheduler's actual render semaphore. Since
		// Go's errgroup hides its acquisition timestamp, the render function
		// records the owner-side interval immediately around execution; the
		// scheduler itself remains the single concurrency owner.
		artifact, err := s.render(s.ctx, task.Plan)
		s.mu.Lock()
		s.results[idx] = TaskResult{Priority: task.Priority, Artifact: artifact, Err: err}
		s.mu.Unlock()
		return nil
	})
	return idx
}

func (s *Scheduler) Wait() []TaskResult {
	if s == nil {
		return nil
	}
	_ = s.g.Wait()
	s.mu.Lock()
	results := make([]TaskResult, len(s.results))
	copy(results, s.results)
	s.mu.Unlock()
	return results
}

// RecordRenderSlotWait is the scheduler-side seam for callers that use a
// queue-backed worker pool with an explicit semaphore. It keeps the wait
// attribution canonical without forcing the scheduler to invent timestamps.
func RecordRenderSlotWait(ctx context.Context, startedAt, finishedAt time.Time) {
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, startedAt, finishedAt, kernobs.StageStatusCompleted, nil)
}

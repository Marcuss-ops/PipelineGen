package localization

import (
	"context"
	"sync"

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

// RecordRenderSlotWait is the scheduler-side seam for callers that use a
// queue-backed worker pool with an explicit semaphore. It keeps the wait
// attribution canonical without forcing the scheduler to invent timestamps.

package wiring

// clip_render_parent_completion.go owns the composition-root side of
// event-driven clip.render parent finalisation.
//
// Two callers need to finalise a clip.render parent:
//
//	(a) the worker, the instant a settle child commits terminal — the healthy
//	    path, which must not wait for anything; and
//	(b) the lifecycle step's ticker, which catches a parent stranded by a crash
//	    between the child commit and the notification.
//
// They share ONE aggregator (ComposeRoot.ClipRenderParentAggregator) so there is
// a single authority for the finalisation rule. Sharing also matters for
// correctness, not just hygiene: the parent flip is an idempotent no-lease CAS,
// so a notification racing the sweep is harmless, but two independent
// constructions would be two places to keep that contract in sync.

import (
	"context"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// clipRenderParentAggregator returns the process-wide clip.render parent
// aggregator, building it once and caching it on the composition root. Returns
// nil when the jobs service is unavailable, which leaves both the notifier and
// the sweeper unwired (fail-open for observability, since neither can strand a
// job that the parent aggregator would otherwise have finalised — a nil
// aggregator means clip rendering was not composed either).
func clipRenderParentAggregator(root *ComposeRoot, log *zap.Logger) *cliprender.ParentAggregator {
	if root == nil {
		return nil
	}
	if root.ClipRenderParentAggregator != nil {
		return root.ClipRenderParentAggregator
	}
	if root.Jobs == nil || root.Jobs.Service == nil {
		return nil
	}
	root.ClipRenderParentAggregator = cliprender.NewParentAggregator(
		root.Jobs.Service, log, cliprender.DefaultParentAggregationInterval)
	return root.ClipRenderParentAggregator
}

// clipRenderParentNotifier adapts the worker's ParentCompletionNotifier port to
// the shared aggregator.
//
// It owns exactly ONE child type: a terminal clip.render child finalises its
// clip.render parent. Every other capability's children fall through untouched,
// because their aggregation semantics differ (voiceover and script have several
// children and their own aggregators) — the port is attached to every worker, so
// the type filter here is what keeps that safe.
type clipRenderParentNotifier struct {
	agg clipRenderParentFinalizer
}

// clipRenderParentFinalizer is the narrow capability surface this adapter
// needs. *cliprender.ParentAggregator satisfies it; the port keeps the adapter
// testable without a live composition root.
type clipRenderParentFinalizer interface {
	FinalizeParent(ctx context.Context, parentJobID string) error
}

var (
	_ appjobs.ParentCompletionNotifier = (*clipRenderParentNotifier)(nil)
	_ clipRenderParentFinalizer        = (*cliprender.ParentAggregator)(nil)
)

func (n *clipRenderParentNotifier) NotifyChildTerminal(ctx context.Context, child *job.Job) error {
	if n == nil || n.agg == nil || child == nil {
		return nil
	}
	if child.Type != cliprender.TypeClipRender || child.ParentJobID == "" {
		return nil
	}
	return n.agg.FinalizeParent(ctx, child.ParentJobID)
}

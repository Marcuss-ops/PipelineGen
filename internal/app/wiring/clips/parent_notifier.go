package clipswiring

import (
	"context"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ParentFinalizer is the narrow aggregator surface used by the worker's
// completion notification adapter.
type ParentFinalizer interface {
	FinalizeParent(ctx context.Context, parentJobID string) error
}

type clipRenderParentNotifier struct {
	agg ParentFinalizer
}

// NewClipRenderParentNotifier routes only terminal clip.render children to
// their family aggregator. Other job types remain owned by their own policy.
func NewClipRenderParentNotifier(agg ParentFinalizer) appjobs.ParentCompletionNotifier {
	return &clipRenderParentNotifier{agg: agg}
}

func (n *clipRenderParentNotifier) NotifyChildTerminal(ctx context.Context, child *job.Job) error {
	if n == nil || n.agg == nil || child == nil {
		return nil
	}
	if child.Type != cliprender.TypeClipRender || child.ParentJobID == "" {
		return nil
	}
	return n.agg.FinalizeParent(ctx, child.ParentJobID)
}

var _ appjobs.ParentCompletionNotifier = (*clipRenderParentNotifier)(nil)

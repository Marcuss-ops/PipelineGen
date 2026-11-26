package scheduling

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// RunParentAggregationSweep is the shared one-tick loop of the script and
// voiceover parent aggregators. The per-capability aggregator owns its
// parent type and its aggregateOne; the polling, logging and error policy
// live here once.
func RunParentAggregationSweep(
	ctx context.Context,
	log *zap.Logger,
	label, parentType string,
	list func(context.Context, string, int) ([]job.Job, error),
	aggregate func(context.Context, job.Job) error,
) {
	if log == nil {
		log = zap.NewNop()
	}
	jobs, err := list(ctx, parentType, 100)
	if err != nil {
		log.Error(label+".Tick: ListAwaitingAggregation failed", zap.Error(err))
		return
	}
	for _, j := range jobs {
		if err := aggregate(ctx, j); err != nil {
			log.Warn(label+".Tick: aggregateOne failed",
				zap.String("parent_job_id", j.ID), zap.Error(err))
		}
	}
}

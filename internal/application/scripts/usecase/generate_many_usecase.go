// Package scripts — generate_many_usecase.go: multi-item fan-out
// via child broker jobs (script.generate_item). The aggregator
// (parent_aggregator.go) collects child outcomes and finalises
// the parent job.
package usecase

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// FanoutItemBroker is the narrow Pattern-0 port for emitting per-item
// child jobs. The canonical *appjobs.Service satisfies it via
// Enqueue(JobPolicy{Type: TypeScriptGenerateItem}).
type FanoutItemBroker interface {
	EnqueueScriptItem(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error)
}

// GenerateManyUseCase fans out multi-item script generation as
// separate child jobs via the wired broker.
type GenerateManyUseCase struct {
	log    *zap.Logger
	broker FanoutItemBroker
}

// NewGenerateManyUseCase constructs the fan-out use case.
func NewGenerateManyUseCase(log *zap.Logger) *GenerateManyUseCase {
	return &GenerateManyUseCase{log: log}
}

// SetFanoutBroker wires the broker port. Must be called before
// ExecuteFanout.
func (uc *GenerateManyUseCase) SetFanoutBroker(broker FanoutItemBroker) {
	if uc == nil {
		return
	}
	uc.broker = broker
}

// FanoutResult carries the outcome of a fan-out operation.
type FanoutResult struct {
	TotalItems         int
	FailedEnqueueCount int
	ChildJobIDs        []string
	TotalEnqueued      int
}

// ExecuteFanout emits each item as a separate script.generate_item child
// job via the wired broker. The aggregator (parent_aggregator.go)
// reads child outcomes and finalises the parent.
func (uc *GenerateManyUseCase) ExecuteFanout(
	ctx context.Context,
	parentJobID string,
	env *scriptpkg.GenerationEnvelopeV2,
) (*FanoutResult, error) {
	if uc == nil {
		return nil, fmt.Errorf("%w: use case not constructed", scriptpkg.ErrGenerationFailed)
	}
	if uc.broker == nil {
		return nil, fmt.Errorf("%w: fanout broker not wired", scriptpkg.ErrGenerationFailed)
	}
	if env == nil || len(env.Items) == 0 {
		return &FanoutResult{}, nil
	}

	n := len(env.Items)
	childJobIDs := make([]string, n)
	enqueueErrors := 0

	for i, item := range env.Items {
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("item-%d", i)
			env.Items[i].ID = itemID
			item.ID = itemID
		}
		if env.ForceRefresh {
			item.ScriptParams.ForceRefresh = true
		}
		if ctx.Err() != nil {
			childJobIDs[i] = ""
			enqueueErrors++
			continue
		}
		jobID, err := uc.broker.EnqueueScriptItem(ctx, parentJobID, i, item, env.Preset)
		if err != nil {
			if uc.log != nil {
				uc.log.Warn("generate-many: child enqueue failed",
					zap.String("item_id", itemID),
					zap.String("parent_job_id", parentJobID),
					zap.Error(err))
			}
			childJobIDs[i] = ""
			enqueueErrors++
			continue
		}
		childJobIDs[i] = jobID
	}

	if enqueueErrors == n && n > 0 {
		return nil, fmt.Errorf("%w: all %d items failed to enqueue",
			scriptpkg.ErrGenerationFailed, n)
	}
	if enqueueErrors > 0 && uc.log != nil {
		uc.log.Warn("generate-many: partial fan-out failure",
			zap.Int("total", n),
			zap.Int("failed_enqueue", enqueueErrors))
	}

	enqueued := n - enqueueErrors

	if uc.log != nil {
		uc.log.Info("generate-many: fanout completed",
			zap.String("parent_job_id", parentJobID),
			zap.Int("total", n),
			zap.Int("failed_enqueue", enqueueErrors),
			zap.Int("enqueued", enqueued))
	}

	return &FanoutResult{
		TotalItems:         n,
		FailedEnqueueCount: enqueueErrors,
		ChildJobIDs:        childJobIDs,
		TotalEnqueued:      enqueued,
	}, nil
}

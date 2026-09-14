// Package scripts — generate_many_usecase.go: multi-item fan-out
// via child broker jobs (script.generate_item). The aggregator
// (parent_aggregator.go) collects child outcomes and finalises
// the parent job.
package usecase

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

const defaultFanoutConcurrency = 4

// FanoutItemBroker is the narrow Pattern-0 port for emitting per-item
// child jobs. The canonical *appjobs.Service satisfies it via
// Enqueue(JobPolicy{Type: TypeScriptGenerateItem}).
type FanoutItemBroker interface {
	EnqueueScriptItem(ctx context.Context, parentJobID string, itemIndex int, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error)
}

// GenerateManyUseCase fans out multi-item script generation as
// separate child jobs via the wired broker.
type GenerateManyUseCase struct {
	log         *zap.Logger
	broker      FanoutItemBroker
	concurrency int
}

// NewGenerateManyUseCase constructs the fan-out use case.
func NewGenerateManyUseCase(log *zap.Logger) *GenerateManyUseCase {
	return &GenerateManyUseCase{log: log, concurrency: defaultFanoutConcurrency}
}

// SetConcurrency bounds how many child enqueue operations may be in flight.
// A non-positive value restores the safe default. Enqueue is an independent
// network/database operation, so serialising it needlessly adds one round trip
// per item to a multi-item script submission while the actual child generation
// remains owned by the worker pool.
func (uc *GenerateManyUseCase) SetConcurrency(value int) {
	if uc == nil {
		return
	}
	if value <= 0 {
		value = defaultFanoutConcurrency
	}
	uc.concurrency = value
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
	PerLanguage        []string
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
	perLanguage := make([]string, n)
	type fanoutItem struct {
		index int
		item  scriptpkg.GenerationItemV2
	}
	type fanoutOutcome struct {
		jobID string
		err   error
	}
	items := make([]fanoutItem, 0, n)
	for i, item := range env.Items {
		perLanguage[i] = item.Language
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("item-%d", i)
			env.Items[i].ID = itemID
			item.ID = itemID
		}
		if env.ForceRefresh {
			item.ScriptParams.ForceRefresh = true
		}
		items = append(items, fanoutItem{index: i, item: item})
	}

	// Map is bounded and returns outcomes in input order. Errors are carried as
	// data rather than returned from the callback so one failed enqueue does not
	// cancel unrelated items: partial fan-out has always been a supported
	// contract and the parent aggregator must see every attempted child.
	workers := uc.concurrency
	if workers <= 0 {
		workers = defaultFanoutConcurrency
	}
	outcomes, mapErr := concurrent.Map(ctx, items, workers, func(workCtx context.Context, _ int, work fanoutItem) (fanoutOutcome, error) {
		if err := workCtx.Err(); err != nil {
			return fanoutOutcome{err: err}, nil
		}
		jobID, err := uc.broker.EnqueueScriptItem(workCtx, parentJobID, work.index, work.item, env.Preset)
		return fanoutOutcome{jobID: jobID, err: err}, nil
	})
	if mapErr != nil {
		return nil, fmt.Errorf("%w: bounded child enqueue: %w", scriptpkg.ErrGenerationFailed, mapErr)
	}

	enqueueErrors := 0
	for i, outcome := range outcomes {
		item := items[i]
		if outcome.err != nil || outcome.jobID == "" {
			if outcome.err == nil {
				outcome.err = fmt.Errorf("broker returned empty child job id")
			}
			if uc.log != nil {
				uc.log.Warn("generate-many: child enqueue failed",
					zap.String("item_id", item.item.ID),
					zap.String("parent_job_id", parentJobID),
					zap.Error(outcome.err))
			}
			childJobIDs[item.index] = ""
			enqueueErrors++
			continue
		}
		childJobIDs[item.index] = outcome.jobID
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
		PerLanguage:        perLanguage,
		TotalEnqueued:      enqueued,
	}, nil
}

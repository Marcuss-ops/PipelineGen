// Package scripts — generate_many_usecase.go orchestrates
// multi-item generation with bounded concurrency, structured
// per-item results, and partial-failure semantics.
//
// Each item in the envelope is processed independently through
// GenerateOneUseCase. The number of concurrent workers is capped
// by adapters.NormalizationConfig.MaxBatchWorkers (default 4). Items
// that fail are recorded as per-item errors — a failing item does
// NOT abort the remaining items (partial-failure schema).
//
// Context cancellation is respected: when ctx is cancelled, the
// loop stops launching new items, but already-running items are
// allowed to complete. Cancelled-but-not-started items are
// recorded with a "context cancelled" error.
//
// P0 #4 audit (audit 2026-07-03): when a broker is wired via
// SetFanoutBroker, the multi-item path emits each item as a separate
// script.generate_item child job (canonical per-item retry via the
// broker). The aggregator (internal/application/scripts/jobs/
// / parent_aggregator.go) then aggregates child outcomes and calls
// FinalizeAggregateParent to set the parent's broker status. The fan-out path
// preserves the legacy inline execution when no broker is wired
// (the canonical backward-compat guarantee for tests and current
// callers).
package usecase

import (
	"context"
	"fmt"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// FanoutItemBroker is the narrow Pattern-0 port for emitting per-item
// child jobs (P0 #4 audit closure). The canonical *appjobs.Service
// satisfies it via Enqueue(JobPolicy{Type: TypeScriptGenerateItem}).
// Tests inject stubs without instantiating the full broker.
type FanoutItemBroker interface {
	EnqueueScriptItem(ctx context.Context, parentJobID string, item scriptpkg.GenerationItemV2, preset scriptpkg.Preset) (string, error)
}

// GenerateManyUseCase orchestrates multi-item generation. Each item
// is processed independently through the unified pipeline, with
// bounded concurrency controlled by adapters.NormalizationConfig.MaxBatchWorkers.
//
// P0 #4: when broker is wired via SetFanoutBroker, multi-item runs
// FAN OUT each item as a child broker job (per-item retry);
// otherwise multi-item runs the legacy INLINE path (backward-compat).
type GenerateManyUseCase struct {
	one    *GenerateOneUseCase
	log    *zap.Logger
	broker FanoutItemBroker
}

// NewGenerateManyUseCase wraps a GenerateOneUseCase for batch.
func NewGenerateManyUseCase(one *GenerateOneUseCase, log *zap.Logger) *GenerateManyUseCase {
	return &GenerateManyUseCase{one: one, log: log}
}

// SetFanoutBroker wires the optional broker port. Callers that want
// the P0 #4 child-job architecture pass a non-nil FanoutItemBroker
// (composition root wires this from the typed *appjobs.Service via
// an adapter). Tests / callers that want the legacy inline path
// leave broker nil.
func (uc *GenerateManyUseCase) SetFanoutBroker(broker FanoutItemBroker) {
	if uc == nil {
		return
	}
	uc.broker = broker
}

// HasFanoutBroker reports whether the canonical FanoutItemBroker
// port is wired. The handler reads this to decide between the
// legacy inline path (broker == nil) and the P0 #4 fan-out path
// (broker != nil). godlike/07 fail-closed: leaving the broker nil
// preserves the legacy backward-compat behavior (existing tests
// and callers continue to work); the fan-out path is opt-in.
func (uc *GenerateManyUseCase) HasFanoutBroker() bool {
	if uc == nil {
		return false
	}
	return uc.broker != nil
}

// ── Result types ───────────────────────────────────────────────────

// GenerateManyItemResult records the outcome of a single item within
// a multi-item run. Exactly one of Result (success) or Error (failure)
// is populated.
type GenerateManyItemResult struct {
	ItemID string                      `json:"item_id"`
	Result *scriptpkg.GenerationResult `json:"result,omitempty"`
	Error  string                      `json:"error,omitempty"`
}

// GenerateManySummary holds aggregate counts for a multi-item run.
type GenerateManySummary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

// GenerateManyResult holds the complete outcome of a multi-item run.
// Items is ordered by input index. Warnings carry non-per-item
// observations (e.g. "one or more items failed").
//
// P0 #4 audit (audit 2026-07-03): ChildJobIDs carries one entry per
// item (empty string for failed enqueues) so the handler can extract
// them when building the parent waiting_children envelope.
type GenerateManyResult struct {
	Items       []GenerateManyItemResult `json:"items"`
	Summary     GenerateManySummary      `json:"summary"`
	Warnings    []string                 `json:"warnings,omitempty"`
	ChildJobIDs []string                 `json:"child_job_ids,omitempty"`
}

// ── Execute ────────────────────────────────────────────────────────

// Execute runs the unified pipeline for every item in the envelope
// with bounded concurrency.  Each item is processed independently;
// a failure in one item does NOT abort the remaining items.
// Per-item errors are recorded in Items[i].Error.
//
// Context cancellation: when ctx is cancelled, the loop stops
// launching new items.  Items that are already running are allowed
// to complete and their results are recorded.  Items that were not
// yet started are recorded with a "context cancelled" error.
//
// Returns GenerateManyResult on partial success (some items failed)
// or complete success.  Returns an error only when the use case
// itself is not constructed or the envelope is nil.
func (uc *GenerateManyUseCase) Execute(
	ctx context.Context,
	env *scriptpkg.GenerationEnvelopeV2,
	cfg adapters.NormalizationConfig,
	progressFn ProgressFn,
) (*GenerateManyResult, error) {
	if env == nil || len(env.Items) == 0 {
		return &GenerateManyResult{
			Items:   []GenerateManyItemResult{},
			Summary: GenerateManySummary{},
		}, nil
	}
	if uc == nil || uc.one == nil {
		return nil, fmt.Errorf("%w: use case not constructed", scriptpkg.ErrGenerationFailed)
	}

	// Determine max concurrency.
	workers := cfg.MaxBatchWorkers
	if workers <= 0 {
		workers = 4 // default
	}

	n := len(env.Items)
	items := make([]GenerateManyItemResult, n)

	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i := 0; i < n; i++ {
		item := env.Items[i]
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("item-%d", i)
		}

		// Check context before acquiring semaphore.
		if ctx.Err() != nil {
			items[i] = GenerateManyItemResult{
				ItemID: itemID,
				Error:  fmt.Sprintf("context cancelled: %v", ctx.Err()),
			}
			continue
		}

		// Acquire semaphore (blocks if at capacity).
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			items[i] = GenerateManyItemResult{
				ItemID: itemID,
				Error:  fmt.Sprintf("context cancelled: %v", ctx.Err()),
			}
			continue
		}

		wg.Add(1)
		go func(idx int, it scriptpkg.GenerationItemV2, id string) {
			defer wg.Done()
			defer func() { <-sem }() // release

			// Check context one more time before work starts.
			if ctx.Err() != nil {
				items[idx] = GenerateManyItemResult{
					ItemID: id,
					Error:  fmt.Sprintf("context cancelled before start: %v", ctx.Err()),
				}
				return
			}

			tracker := NewProgressTracker(progressFn, id)
			result, err := uc.one.Execute(ctx, it, env.Preset, tracker)

			if err != nil {
				items[idx] = GenerateManyItemResult{
					ItemID: id,
					Error:  err.Error(),
				}
				if uc.log != nil {
					uc.log.Warn("generate-many: item failed",
						zap.String("item_id", id),
						zap.Error(err))
				}
				return
			}
			items[idx] = GenerateManyItemResult{
				ItemID: id,
				Result: result,
			}
		}(i, item, itemID)
	}

	wg.Wait()

	// Compile aggregate.
	var succeeded, failed int
	var warnings []string
	for i := range items {
		if items[i].Error != "" {
			failed++
		} else {
			succeeded++
		}
	}
	if failed > 0 {
		warnings = append(warnings,
			fmt.Sprintf("%d of %d items failed", failed, n))
	}

	if uc.log != nil {
		uc.log.Info("generate-many: completed",
			zap.Int("total", n),
			zap.Int("succeeded", succeeded),
			zap.Int("failed", failed))
	}

	return &GenerateManyResult{
		Items:    items,
		Summary:  GenerateManySummary{Total: n, Succeeded: succeeded, Failed: failed},
		Warnings: warnings,
	}, nil
}

// ExecuteFanout emits each item as a separate script.generate_item child
// job via the wired broker. The aggregator (parent_aggregator.go)
// reads child outcomes and FinalizeAggregateParent-s the parent's broker status.
// The returned GenerateManyResult carries ChildJobIDs (one per item,
// empty string for failed enqueues) and Summary counts derived from
// the fanout outcome (Total = children count, Succeeded/Failed = 0
// initially because final outcomes are decided by the aggregator).
//
// P0 #4 audit closure (mirror of voiceover FanoutVoiceoversUseCase):
// this is the canonical fan-out use case for the per-item retry
// semantic. The handler (generation_job.go Handle multi-item path)
// reads ExecOrFanout to pick the path: inline (broker nil) vs fan-out.
//
// godlike/07 fail-closed: a non-nil broker with a partial fan-out
// failure is returned as a non-nil Go error so the worker treats
// it as FAILED (matches the voiceover FanoutVoiceoversUseCase).
func (uc *GenerateManyUseCase) ExecuteFanout(
	ctx context.Context,
	parentJobID string,
	env *scriptpkg.GenerationEnvelopeV2,
) (*GenerateManyResult, error) {
	if uc == nil {
		return nil, fmt.Errorf("%w: use case not constructed", scriptpkg.ErrGenerationFailed)
	}
	if uc.broker == nil {
		return nil, fmt.Errorf("%w: fanout broker not wired", scriptpkg.ErrGenerationFailed)
	}
	if env == nil || len(env.Items) == 0 {
		return &GenerateManyResult{
			Items:   []GenerateManyItemResult{},
			Summary: GenerateManySummary{},
		}, nil
	}

	n := len(env.Items)
	items := make([]GenerateManyItemResult, n)
	childJobIDs := make([]string, n)
	enqueueErrors := 0

	for i, item := range env.Items {
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("item-%d", i)
		}
		if ctx.Err() != nil {
			items[i] = GenerateManyItemResult{
				ItemID: itemID,
				Error:  fmt.Sprintf("context cancelled before fanout: %v", ctx.Err()),
			}
			enqueueErrors++
			continue
		}
		jobID, err := uc.broker.EnqueueScriptItem(ctx, parentJobID, item, env.Preset)
		if err != nil {
			if uc.log != nil {
				uc.log.Warn("generate-many: child enqueue failed (P0 #4)",
					zap.String("item_id", itemID),
					zap.String("parent_job_id", parentJobID),
					zap.Error(err))
			}
			items[i] = GenerateManyItemResult{
				ItemID: itemID,
				Error:  fmt.Sprintf("enqueue failed: %v", err),
			}
			childJobIDs[i] = ""
			enqueueErrors++
			continue
		}
		childJobIDs[i] = jobID
		// Items is left as a placeholder (Result=nil, Error=""); the
		// aggregator will overwrite child results on terminal flip.
		items[i] = GenerateManyItemResult{ItemID: itemID}
	}

	// Fan-out is considered FAILED when ALL enqueues fail. Returns
	// a typed error so the worker marks the parent FAILED (Commit 2
	// P0 #4 — no fake availability). Partial failures are logged and
	// the parent proceeds to waiting_children; the aggregator tracks
	// the failed_enqueue_count in the parent result.
	if enqueueErrors == n && n > 0 {
		return nil, fmt.Errorf("%w: all %d items failed to enqueue (P0 #4 fan-out failure)", scriptpkg.ErrGenerationFailed, n)
	}
	if enqueueErrors > 0 {
		if uc.log != nil {
			uc.log.Warn("generate-many: partial fan-out failure",
				zap.Int("total", n),
				zap.Int("failed_enqueue", enqueueErrors))
		}
	}

	var warnings []string
	if enqueueErrors > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of %d items failed to enqueue", enqueueErrors, n))
	}

	if uc.log != nil {
		uc.log.Info("generate-many: fanout completed",
			zap.String("parent_job_id", parentJobID),
			zap.Int("total", n),
			zap.Int("failed_enqueue", enqueueErrors),
			zap.Int("child_job_ids_len", len(childJobIDs)))
	}

	// Return a GenerateManyResult with ChildJobIDs so the handler can
	// extract them when building the parent waiting_children envelope.
	// Summary counts reflect enqueue outcome; the aggregator decides
	// the final parent outcome (P0 #4 audit closure).
	return &GenerateManyResult{
		Items:       items,
		Summary:     GenerateManySummary{Total: n, Succeeded: n - enqueueErrors, Failed: enqueueErrors},
		Warnings:    warnings,
		ChildJobIDs: childJobIDs,
	}, nil
}

// Package scripts — generate_many_usecase.go orchestrates
// multi-item generation with bounded concurrency, structured
// per-item results, and partial-failure semantics.
//
// Each item in the envelope is processed independently through
// GenerateOneUseCase. The number of concurrent workers is capped
// by NormalizationConfig.MaxBatchWorkers (default 4). Items
// that fail are recorded as per-item errors — a failing item does
// NOT abort the remaining items (partial-failure schema).
//
// Context cancellation is respected: when ctx is cancelled, the
// loop stops launching new items, but already-running items are
// allowed to complete. Cancelled-but-not-started items are
// recorded with a "context cancelled" error.
package scripts

import (
	"context"
	"fmt"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// GenerateManyUseCase orchestrates multi-item generation. Each item
// is processed independently through the unified pipeline, with
// bounded concurrency controlled by NormalizationConfig.MaxBatchWorkers.
type GenerateManyUseCase struct {
	one *GenerateOneUseCase
	log *zap.Logger
}

// NewGenerateManyUseCase wraps a GenerateOneUseCase for batch.
func NewGenerateManyUseCase(one *GenerateOneUseCase, log *zap.Logger) *GenerateManyUseCase {
	return &GenerateManyUseCase{one: one, log: log}
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
type GenerateManyResult struct {
	Items    []GenerateManyItemResult `json:"items"`
	Summary  GenerateManySummary      `json:"summary"`
	Warnings []string                 `json:"warnings,omitempty"`
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
	cfg NormalizationConfig,
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

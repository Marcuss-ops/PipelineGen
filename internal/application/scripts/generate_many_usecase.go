// Package scripts — generate_many_usecase.go orchestrates
// multi-item generation (the batch path). It calls
// GenerateOneUseCase for each item independently, collecting
// results and errors.
//
// The batch does NOT have its own prompt, cache, or generation
// logic — it delegates every item to GenerateOneUseCase.
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// GenerateManyUseCase orchestrates multi-item generation. Each item
// is processed independently through the unified pipeline.
type GenerateManyUseCase struct {
	one *GenerateOneUseCase
	log *zap.Logger
}

// NewGenerateManyUseCase wraps a GenerateOneUseCase for batch.
func NewGenerateManyUseCase(one *GenerateOneUseCase, log *zap.Logger) *GenerateManyUseCase {
	return &GenerateManyUseCase{one: one, log: log}
}

// GenerateManyResult holds the outcome of a multi-item run.
type GenerateManyResult struct {
	Results  []*scriptpkg.GenerationResult
	Warnings []string
}

// Execute runs the unified pipeline for every item in the envelope.
// Each item is processed independently; a failure in one item does
// not abort the remaining items. Per-item errors are collected as
// warnings in the result, and the failed item's GenerationResult
// is omitted.
//
// Returns GenerateManyResult on partial success (some items failed)
// or complete success. Returns an error only when the use case
// itself is not constructed.
func (uc *GenerateManyUseCase) Execute(
	ctx context.Context,
	env *scriptpkg.GenerationEnvelopeV2,
	cfg NormalizationConfig,
	progressFn ProgressFn,
) (*GenerateManyResult, error) {
	if uc == nil || uc.one == nil {
		return nil, fmt.Errorf("%w: use case not constructed", scriptpkg.ErrGenerationFailed)
	}
	if env == nil || len(env.Items) == 0 {
		return &GenerateManyResult{}, nil
	}

	results := make([]*scriptpkg.GenerationResult, 0, len(env.Items))
	var warnings []string

	for i, item := range env.Items {
		itemID := item.ID
		if itemID == "" {
			itemID = fmt.Sprintf("item-%d", i)
		}

		tracker := NewProgressTracker(progressFn, itemID)

		result, err := uc.one.Execute(ctx, item, env.Preset, tracker)
		if err != nil {
			warn := fmt.Sprintf("%s: %v", itemID, err)
			warnings = append(warnings, warn)
			if uc.log != nil {
				uc.log.Warn("generate-many: item failed, continuing with next",
					zap.String("item_id", itemID),
					zap.Error(err))
			}
			continue
		}
		results = append(results, result)
	}

	if len(results) == 0 && len(warnings) > 0 {
		return &GenerateManyResult{
			Warnings: warnings,
		}, fmt.Errorf("%w: all %d items failed (first: %s)",
			scriptpkg.ErrGenerationFailed, len(env.Items), warnings[0])
	}

	return &GenerateManyResult{
		Results:  results,
		Warnings: warnings,
	}, nil
}

package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Run: the canonical postprocessor pipeline ─────────────────────────

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order.
func (r *PostProcessorRegistry) Run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PipelineResult, error) {
	if r == nil {
		return &PipelineResult{}, nil
	}
	r.mu.RLock()
	procs := make(map[ProcessorName]PostProcessor, len(r.processors))
	for k, v := range r.processors {
		procs[k] = v
	}
	policies := make(map[ProcessorName]ProcessorPolicy, len(r.policies))
	for k, v := range r.policies {
		policies[k] = v
	}
	r.mu.RUnlock()

	if len(plan.Postprocessors) == 0 {
		return &PipelineResult{FinalSpecScene: input.SpecScene}, nil
	}

	result := &PipelineResult{
		StageDurations: make(map[string]int64),
	}
	// Issue #1 (June 2026): seed FinalSpecScene with the
	// pre-walk envelope so buildGenerationResult's empty-aware
	// fallback sees a populated surface even when the loop
	// short-circuits before calling mergePostProcessResult
	// (empty-plan early return already covered above; processor
	// outcomes that IsEmpty()==true also skip merge here). The
	// mergePostProcessResult hook below overwrites this seed
	// with the post-walk envelope whenever a processor
	// successfully returns a non-empty result, so capturing
	// currentInput.SpecScene acts as the canonical "last writer
	// wins" snapshot at the post-walk time.
	result.FinalSpecScene = input.SpecScene
	var (
		warnings          []string
		requiredRequested int
		requiredSucceeded int
		requiredFails     []string
	)

	for _, rawName := range plan.Postprocessors {
		name := ProcessorName(rawName)
		proc, ok := procs[name]
		policy := policies[name]
		if policy == "" {
			policy = DefaultPolicyFor(name)
		}

		if !ok || proc == nil {
			warn := fmt.Sprintf("postprocessor %q not registered", string(name))
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (not registered)")
			} else if r.log != nil {
				r.log.Warn("postprocessor not registered, skipping (best-effort)",
					zap.String("name", string(name)),
					zap.String("item_id", plan.ID))
			}
			continue
		}

		start := time.Now()
		ppResult, err := proc.Process(ctx, plan, input)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			result.StageDurations[string(name)] = elapsed
			warn := fmt.Sprintf("postprocessor %q failed: %v", string(name), err)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (failed: "+err.Error()+")")
			}
			if r.log != nil {
				r.log.Warn("postprocessor outcome",
					zap.String("name", string(name)),
					zap.Error(err))
			}
			continue
		}

		if ppResult == nil {
			result.StageDurations[string(name)] = elapsed
			warn := fmt.Sprintf("postprocessor %q returned nil result", string(name))
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (nil result)")
			}
			continue
		}

		ppResult.DurationMs = elapsed
		result.StageDurations[string(name)] = elapsed

		if ppResult.IsEmpty() {
			warn := fmt.Sprintf("postprocessor %q returned empty output", string(name))
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (empty output)")
			}
			continue
		}

		if policy == ProcessorRequired {
			requiredRequested++
			requiredSucceeded++
		}

		mergePostProcessResult(result, ppResult, &input)

		if len(ppResult.Warnings) > 0 {
			warnings = append(warnings, ppResult.Warnings...)
		}
	}

	result.Warnings = warnings
	// Issue 3 / P0 (June 2026): the gate flipped.
	//
	// Pre-fix: a partial-success pattern (one Required processor
	// succeeds + another Required processor fails) was reported as
	// success because the gate was `requiredRequested > 0 &&
	// requiredSucceeded == 0`. This violated the ProcessorRequired
	// contract — any Required-class failure must abort the
	// pipeline, regardless of how many other Required processors
	// succeeded.
	//
	// The new gate is `len(requiredFails) > 0`: ANY Required-class
	// failure (err / nil-result / empty-output / missing-registry)
	// surfaces as a Go error wrapping
	// scriptpkg.ErrPostprocessFailed. The pre-fix "all required
	// failed" semantic is preserved as a strict subset (k-of-n
	// failures now fire the gate just as well as n-of-n failures).
	if len(requiredFails) > 0 {
		return result, fmt.Errorf("%w: required postprocessor failure: %s",
			scriptpkg.ErrPostprocessFailed, strings.Join(requiredFails, "; "))
	}
	return result, nil
}

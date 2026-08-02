package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

const postprocessorOperationTimeout = 5 * time.Minute

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

	// Concurrency safety: the caller's ProcessInput may share
	// SpecScene slices with other goroutines (e.g. a cached
	// engineResult.Output). Deep-clone before mergePostProcessResult
	// mutates Scenes / Bindings in place so concurrent Runs cannot
	// race on the same underlying memory.
	input.SpecScene = cloneSpecSceneOutput(input.SpecScene)

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
		// A processor may require a stronger policy only for plans that
		// explicitly activate its capability. Keep the registered default as
		// the compatibility fallback for inactive plans.
		if proc != nil {
			if runtimePolicy := proc.Policy(plan); runtimePolicy != "" {
				policy = runtimePolicy
			}
		}
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
		processorCtx, cancel := context.WithTimeout(ctx, postprocessorOperationTimeout)
		ppResult, err := proc.Process(processorCtx, plan, input)
		cancel()
		elapsed := time.Since(start).Milliseconds()
		if timing := r.vidRushTimingMetrics(); timing != nil {
			timing.ObserveProcessorDuration(string(name), float64(elapsed)/1000)
		}
		// Concurrency safety: a processor may return a shared/cached
		// PostProcessResult (common in stubs and caches). Clone before
		// mutating DurationMs or passing to merge so concurrent Run
		// calls cannot race on the same pointer.
		ppResult = clonePostProcessResult(ppResult)

		if err != nil {
			result.StageDurations[string(name)] = elapsed
			warn := fmt.Sprintf("postprocessor %q failed: %v", string(name), err)
			warnings = append(warnings, warn)
			if ppResult != nil && !ppResult.IsEmpty() {
				// A processor may return a fail-closed UpdatedSpecScene
				// together with its error. Merge that safe surface before
				// deciding whether the walk can continue.
				mergePostProcessResult(result, ppResult, &input)
				if len(ppResult.Warnings) > 0 {
					warnings = append(warnings, ppResult.Warnings...)
				}
			}
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (failed: "+err.Error()+")")
				if name == ProcessorAssetLocationReconciliation {
					// A failed location gate must never allow document or
					// persistence to publish the pre-gate bindings. The
					// processor has already supplied the fail-closed scene;
					// return immediately with the typed error.
					result.Warnings = warnings
					return result, fmt.Errorf("%w: required postprocessor failure: %s",
						scriptpkg.ErrPostprocessFailed, strings.Join(requiredFails, "; "))
				}
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

	// Single-segment documentary prose: best-effort post-processors
	// (clip_bindings / stock_bindings / visual / voiceover) commonly
	// emit a "returned empty output" / "not registered" / "returned
	// nil result" warning when the source shape has only one scene
	// with no clip evidence, no stock query, and full source-text
	// coverage. These are spurious SUCCEEDED_WITH_WARNINGS triggers
	// when the plan is documentary prose (SingleScene=true, no
	// strict grounding policy, non-empty SourceText). Filter them
	// out before the canonical status classifier runs so it reports
	// SUCCEEDED instead of SUCCEEDED_WITH_WARNINGS.
	if plan != nil && plan.SingleScene &&
		plan.GroundingPolicy == "" &&
		strings.TrimSpace(plan.SourceText) != "" {
		warnings = filterBestEffortDocumentaryWarnings(warnings)
	}
	// Single-segment text plan with a populated Stock binding in
	// SpecScene: ClipSearchProcessor may have emitted
	// "clip_search: no matching Artlist clips found for segment X"
	// / "clip_search: ArtlistClipSearcher not configured" soft
	// warnings when the Artlist searcher returned no candidates
	// (and the plan's ProviderPolicy did not opt in). Those
	// warnings are spurious — StockBindingsProcessor landed a real
	// binding in the post-walk SclipScene (Stock.DriveLink or
	// Stock.AssetID populated), so the operator observability
	// signal is "binding is present in JSON" rather than
	// "warning: no clips found". Drop clip_search: prefixed lines
	// in this case so the canonical status classifier reports
	// SUCCEEDED instead of SUCCEEDED_WITH_WARNINGS.
	if plan != nil && plan.SingleScene && plan.SourceKind == "text" &&
		len(result.FinalSpecScene.Scenes) > 0 {
		stock := result.FinalSpecScene.Scenes[0].Bindings.Stock
		if stock != nil &&
			(strings.TrimSpace(stock.DriveLink) != "" ||
				strings.TrimSpace(stock.AssetID) != "") {
			warnings = filterBestEffortBindingWarnings(warnings)
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

// cloneSpecSceneOutput returns a deep copy of the specscene envelope.
// Run() needs an independent copy because mergePostProcessResult
// mutates Scenes and Bindings in place; without cloning, concurrent
// Runs operating on the same cached engine output would race on the
// same underlying slices and pointer fields.
func cloneSpecSceneOutput(s scriptpkg.SpecSceneOutput) scriptpkg.SpecSceneOutput {
	if len(s.Scenes) == 0 {
		return s
	}
	out := scriptpkg.SpecSceneOutput{
		Version: s.Version,
		Scenes:  make([]scriptpkg.SpecScene, len(s.Scenes)),
	}
	for i, sc := range s.Scenes {
		out.Scenes[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			out.Scenes[i].Metadata = &meta
		}
		out.Scenes[i].Bindings = cloneSceneBindings(sc.Bindings)
	}
	return out
}

// cloneSceneBindings returns a deep copy of bindings so that in-place
// mutations of Image / Voiceover / Clip / Stock pointers in one Run
// do not affect another Run sharing the same underlying scene.
func cloneSceneBindings(b scriptpkg.SceneBindings) scriptpkg.SceneBindings {
	out := scriptpkg.SceneBindings{}
	if len(b.Media) > 0 {
		out.Media = make([]scriptpkg.ResolvedMediaBinding, len(b.Media))
		copy(out.Media, b.Media)
	}
	if b.Clip != nil {
		c := *b.Clip
		out.Clip = &c
	}
	if b.Image != nil {
		img := *b.Image
		out.Image = &img
	}
	if b.Voiceover != nil {
		v := *b.Voiceover
		out.Voiceover = &v
	}
	if b.Stock != nil {
		s := *b.Stock
		out.Stock = &s
	}
	return out
}

// clonePostProcessResult returns a shallow copy of r. It is nil-safe
// and isolates the registry's per-run mutations (DurationMs) from a
// processor's shared/cached result pointer.
func clonePostProcessResult(r *PostProcessResult) *PostProcessResult {
	if r == nil {
		return nil
	}
	copy := *r
	return &copy
}

// filterBestEffortDocumentaryWarnings drops the warning lines that
// best-effort postprocessors emit on the single-segment documentary
// shape ("postprocessor %q not registered",
// "postprocessor %q returned nil result",
// "postprocessor %q returned empty output"). Other warnings
// (Required-class failures, clip-native contract violations,
// quality-gate fragments, etc.) pass through untouched.
func filterBestEffortDocumentaryWarnings(w []string) []string {
	out := make([]string, 0, len(w))
	for _, line := range w {
		if strings.HasPrefix(line, "postprocessor ") &&
			(strings.HasSuffix(line, " not registered") ||
				strings.HasSuffix(line, " returned nil result") ||
				strings.HasSuffix(line, " returned empty output")) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// filterBestEffortBindingWarnings drops the ClipSearch soft-warnings
// when a Stock binding has legitimately populated the post-walk
// SpecScene. The composite invokes this on the SingleScene + text
// + Stock-present branch above; clip_search: prefixed lines are
// signals ("no Artlist hits for segment X", "ArtlistClipSearcher
// not configured") that lose relevance once a direct Stock binding
// confirms downstream binding presence. Other warning lines (any
// non-clip_search: prefix) pass through untouched so unrelated
// signals (required-class failures, hard errors, etc.) remain
// observable.
func filterBestEffortBindingWarnings(w []string) []string {
	out := make([]string, 0, len(w))
	for _, line := range w {
		if strings.HasPrefix(line, "clip_search: ") {
			continue
		}
		out = append(out, line)
	}
	return out
}

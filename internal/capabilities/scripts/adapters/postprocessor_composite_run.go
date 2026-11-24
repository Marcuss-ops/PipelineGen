package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

const postprocessorOperationTimeout = 5 * time.Minute

// ProcessorProgressEvent describes an actually executed postprocessor. The
// registry emits these events at the execution boundary, rather than asking a
// caller to announce the whole plan in advance.
type ProcessorProgressEvent struct {
	Index    int
	Total    int
	Name     ProcessorName
	Status   string // started, completed, failed
	Duration time.Duration
	Err      error
}

type ProcessorProgressReporter func(ProcessorProgressEvent)

func reportProcessorProgress(reporter ProcessorProgressReporter, event ProcessorProgressEvent) {
	if reporter != nil {
		reporter(event)
	}
}

// ── Run: the canonical postprocessor pipeline ─────────────────────────

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order.
func (r *PostProcessorRegistry) Run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PipelineResult, error) {
	return r.run(ctx, plan, input, nil)
}

// RunWithProgress is the canonical execution path for callers that expose
// live progress. The callback is per invocation, so concurrent jobs cannot
// overwrite one another's progress sink.
func (r *PostProcessorRegistry) RunWithProgress(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
	reporter ProcessorProgressReporter,
) (*PipelineResult, error) {
	return r.run(ctx, plan, input, reporter)
}

func (r *PostProcessorRegistry) run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
	reporter ProcessorProgressReporter,
) (*PipelineResult, error) {
	if r == nil {
		return &PipelineResult{}, nil
	}
	procs, policies := r.snapshotProcessors()
	return r.runSnapshot(ctx, plan, input, reporter, procs, policies)
}

// snapshotProcessors freezes registry membership for one run. Registrations
// made concurrently apply to the next run, never halfway through this one.
func (r *PostProcessorRegistry) snapshotProcessors() (map[ProcessorName]PostProcessor, map[ProcessorName]ProcessorPolicy) {
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
	return procs, policies
}

func (r *PostProcessorRegistry) runSnapshot(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
	reporter ProcessorProgressReporter,
	procs map[ProcessorName]PostProcessor,
	policies map[ProcessorName]ProcessorPolicy,
) (*PipelineResult, error) {
	// Concurrency safety: the caller's ProcessInput may share
	// SpecScene slices with other goroutines (e.g. a cached
	// engineResult.Output). Deep-clone before MergePostProcessResult
	// mutates Scenes / Bindings in place so concurrent Runs cannot
	// race on the same underlying memory.
	input.SpecScene = cloneSpecSceneOutput(input.SpecScene)
	if len(plan.Postprocessors) == 0 {
		if sanitizer := procs[ProcessorNarrationSanitizer]; sanitizer != nil {
			if err := runNarrationSanitizer(ctx, plan, sanitizer, &input, &PipelineResult{}); err != nil {
				return nil, err
			}
		}
		return &PipelineResult{FinalSpecScene: input.SpecScene}, nil
	}

	result := &PipelineResult{StageProgress: make(map[string]job.StageProgress)}
	// Issue #1 (June 2026): seed FinalSpecScene with the
	// pre-walk envelope so buildGenerationResult's empty-aware
	// fallback sees a populated surface even when the loop
	// short-circuits before calling MergePostProcessResult
	// (empty-plan early return already covered above; processor
	// outcomes that IsEmpty()==true also skip merge here). The
	// MergePostProcessResult hook below overwrites this seed
	// with the post-walk envelope whenever a processor
	// successfully returns a non-empty result, so capturing
	// currentInput.SpecScene acts as the canonical "last writer
	// wins" snapshot at the post-walk time.
	result.FinalSpecScene = input.SpecScene
	if sanitizer := procs[ProcessorNarrationSanitizer]; sanitizer != nil {
		if err := runNarrationSanitizer(ctx, plan, sanitizer, &input, result); err != nil {
			return nil, err
		}
	}
	state := &processorRunState{}
	for index, rawName := range plan.Postprocessors {
		if err := r.runOneProcessor(ctx, plan, &input, result, procs, policies, reporter, index, rawName, state); err != nil {
			result.Warnings = state.warnings
			return result, err
		}
	}
	warnings := finalizeRunWarnings(plan, result, state.warnings)
	result.Warnings = warnings
	if len(state.requiredFails) > 0 {
		return result, fmt.Errorf("%w: required postprocessor failure: %s",
			scriptpkg.ErrPostprocessFailed, strings.Join(state.requiredFails, "; "))
	}
	return result, nil
}

// processorRunState carries the per-walk warnings and Required-processor
// outcomes that outlive one processor invocation.
type processorRunState struct {
	warnings          []string
	requiredRequested int
	requiredSucceeded int
	requiredFails     []string
}

// runOneProcessor executes one postprocessor and records its outcome in state.
func (r *PostProcessorRegistry) runOneProcessor(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input *ProcessInput,
	result *PipelineResult,
	procs map[ProcessorName]PostProcessor,
	policies map[ProcessorName]ProcessorPolicy,
	reporter ProcessorProgressReporter,
	index int,
	rawName string,
	state *processorRunState,
) error {
	name := ProcessorName(rawName)
	proc, ok := procs[name]
	policy := processorPolicyFor(name, proc, policies[name], plan)
	if !ok || proc == nil {
		return r.recordUnregisteredProcessor(plan, reporter, index, name, policy, state)
	}
	reportProcessorProgress(reporter, ProcessorProgressEvent{
		Index: index, Total: len(plan.Postprocessors), Name: name, Status: "started",
	})

	// Restore the translated scene surface before the next processor
	// consumes it. This is deliberately before Process, never after the
	// merge: the final entities pass must be able to annotate the exact
	// text that will be persisted without being overwritten by an older
	// translated snapshot.
	reapplyTranslatedSceneText(input)
	if sanitizer := procs[ProcessorNarrationSanitizer]; sanitizer != nil && name != ProcessorNarrationSanitizer {
		if err := runNarrationSanitizer(ctx, plan, sanitizer, input, result); err != nil {
			return err
		}
	}
	stageReport, ppResult, err := r.invokePostProcessor(ctx, plan, *input, name, proc, result)
	return r.recordProcessorOutcome(plan, input, result, reporter, index, name, policy, stageReport, ppResult, err, state)
}

func processorPolicyFor(name ProcessorName, proc PostProcessor, registered ProcessorPolicy, plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	policy := registered
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
	return policy
}

func (r *PostProcessorRegistry) recordUnregisteredProcessor(plan *scriptpkg.ResolvedGenerationPlan, reporter ProcessorProgressReporter, index int, name ProcessorName, policy ProcessorPolicy, state *processorRunState) error {
	reportProcessorProgress(reporter, ProcessorProgressEvent{
		Index: index, Total: len(plan.Postprocessors), Name: name,
		Status: "failed", Err: fmt.Errorf("postprocessor not registered"),
	})
	state.warnings = append(state.warnings, fmt.Sprintf("postprocessor %q not registered", string(name)))
	if policy == ProcessorRequired {
		state.requiredRequested++
		state.requiredFails = append(state.requiredFails, string(name)+" (not registered)")
	} else if r.log != nil {
		r.log.Warn("postprocessor not registered, skipping (best-effort)",
			zap.String("name", string(name)), zap.String("item_id", plan.ID))
	}
	return nil
}

func (r *PostProcessorRegistry) recordProcessorOutcome(plan *scriptpkg.ResolvedGenerationPlan, input *ProcessInput, result *PipelineResult, reporter ProcessorProgressReporter, index int, name ProcessorName, policy ProcessorPolicy, stageReport kernobs.StageReport, ppResult *PostProcessResult, err error, state *processorRunState) error {
	if err != nil {
		return r.recordProcessorFailure(plan, input, result, reporter, index, name, policy, stageReport, ppResult, err, state)
	}
	if ppResult == nil || ppResult.IsEmpty() {
		reason := "empty output"
		if ppResult == nil {
			reason = "nil result"
		}
		return r.recordEmptyProcessorResult(plan, input, result, reporter, index, name, policy, stageReport, reason, state)
	}
	return r.recordSuccessfulProcessor(plan, input, result, reporter, index, name, policy, stageReport, ppResult, state)
}

func (r *PostProcessorRegistry) recordProcessorFailure(plan *scriptpkg.ResolvedGenerationPlan, input *ProcessInput, result *PipelineResult, reporter ProcessorProgressReporter, index int, name ProcessorName, policy ProcessorPolicy, stageReport kernobs.StageReport, ppResult *PostProcessResult, err error, state *processorRunState) error {
	reportProcessorProgress(reporter, ProcessorProgressEvent{Index: index, Total: len(plan.Postprocessors), Name: name, Status: "failed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond, Err: err})
	recordProcessorProgress(result, name, plan, *input, job.StageFailed, plan.ID, err.Error())
	state.warnings = append(state.warnings, fmt.Sprintf("postprocessor %q failed: %v", string(name), err))
	if ppResult != nil && !ppResult.IsEmpty() {
		// Preserve a fail-closed scene returned together with an error.
		MergePostProcessResult(result, ppResult, input)
		state.warnings = append(state.warnings, ppResult.Warnings...)
	}
	if policy == ProcessorRequired {
		state.requiredRequested++
		state.requiredFails = append(state.requiredFails, string(name)+" (failed: "+err.Error()+")")
		if name == ProcessorAssetLocationReconciliation {
			result.Warnings = state.warnings
			return fmt.Errorf("%w: required postprocessor failure: %s", scriptpkg.ErrPostprocessFailed, strings.Join(state.requiredFails, "; "))
		}
	}
	if r.log != nil {
		r.log.Warn("postprocessor outcome", zap.String("name", string(name)), zap.Error(err))
	}
	return nil
}

func (r *PostProcessorRegistry) recordEmptyProcessorResult(plan *scriptpkg.ResolvedGenerationPlan, input *ProcessInput, result *PipelineResult, reporter ProcessorProgressReporter, index int, name ProcessorName, policy ProcessorPolicy, stageReport kernobs.StageReport, reason string, state *processorRunState) error {
	reportProcessorProgress(reporter, ProcessorProgressEvent{Index: index, Total: len(plan.Postprocessors), Name: name, Status: "failed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond, Err: fmt.Errorf("%s", reason)})
	recordProcessorProgress(result, name, plan, *input, job.StageFailed, plan.ID, reason)
	state.warnings = append(state.warnings, fmt.Sprintf("postprocessor %q returned %s", string(name), reason))
	if policy == ProcessorRequired {
		state.requiredRequested++
		state.requiredFails = append(state.requiredFails, string(name)+" ("+reason+")")
	}
	return nil
}

func (r *PostProcessorRegistry) recordSuccessfulProcessor(plan *scriptpkg.ResolvedGenerationPlan, input *ProcessInput, result *PipelineResult, reporter ProcessorProgressReporter, index int, name ProcessorName, policy ProcessorPolicy, stageReport kernobs.StageReport, ppResult *PostProcessResult, state *processorRunState) error {
	recordProcessorProgress(result, name, plan, *input, job.StageCompleted, plan.ID, "")
	reportProcessorProgress(reporter, ProcessorProgressEvent{
		Index: index, Total: len(plan.Postprocessors), Name: name,
		Status: "completed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond,
	})
	if policy == ProcessorRequired {
		state.requiredRequested++
		state.requiredSucceeded++
	}

	MergePostProcessResult(result, ppResult, input)
	if r.log != nil {
		segments, candidates := vidRushPipelineCounts(input.VidRushSegments)
		r.log.Debug("postprocessor VidRush scene surface",
			zap.String("name", string(name)),
			zap.Int("segments", segments),
			zap.Int("candidates", candidates),
			zap.String("segment_details", vidRushPipelineSegmentDetails(input.VidRushSegments)),
		)
	}

	if len(ppResult.Warnings) > 0 {
		state.warnings = append(state.warnings, ppResult.Warnings...)
	}

	return nil
}

// finalizeRunWarnings applies the documentary and stock-binding warning
// filters that cannot change the processor walk itself.
func finalizeRunWarnings(plan *scriptpkg.ResolvedGenerationPlan, result *PipelineResult, warnings []string) []string {
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
	return warnings
}

func vidRushPipelineCounts(segments []scriptpkg.VidRushSegmentResult) (int, int) {
	candidates := 0
	for _, segment := range segments {
		candidates += len(segment.Assets.Candidates)
	}
	return len(segments), candidates
}

func vidRushPipelineSegmentDetails(segments []scriptpkg.VidRushSegmentResult) string {
	details := make([]string, 0, len(segments))
	for _, segment := range segments {
		details = append(details, fmt.Sprintf("%s/%s q=%d candidates=%d secondary=%d generated=%d ready=%d", segment.SegmentID, segment.SceneID, len(segment.Insights.ImageQueries), len(segment.Assets.Candidates), len(segment.Assets.SecondaryImages), len(segment.Assets.GeneratedImages), countReadyVidRushCandidates(segment.Assets.Candidates)))
	}
	return strings.Join(details, " | ")
}

func countReadyVidRushCandidates(candidates []scriptpkg.SegmentAssetCandidate) int {
	count := 0
	for _, candidate := range candidates {
		if readyVidRushCandidate(candidate) {
			count++
		}
	}
	return count
}

func runNarrationSanitizer(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, proc PostProcessor, input *ProcessInput, result *PipelineResult) error {
	ppResult, err := proc.Process(ctx, plan, *input)
	if err != nil {
		return err
	}
	MergePostProcessResult(result, ppResult, input)
	return nil
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

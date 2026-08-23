package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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

	// Concurrency safety: the caller's ProcessInput may share
	// SpecScene slices with other goroutines (e.g. a cached
	// engineResult.Output). Deep-clone before mergePostProcessResult
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
	// short-circuits before calling mergePostProcessResult
	// (empty-plan early return already covered above; processor
	// outcomes that IsEmpty()==true also skip merge here). The
	// mergePostProcessResult hook below overwrites this seed
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
	var (
		warnings          []string
		requiredRequested int
		requiredSucceeded int
		requiredFails     []string
	)

	for index, rawName := range plan.Postprocessors {
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
			reportProcessorProgress(reporter, ProcessorProgressEvent{
				Index: index, Total: len(plan.Postprocessors), Name: name,
				Status: "failed", Err: fmt.Errorf("postprocessor not registered"),
			})
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
		reportProcessorProgress(reporter, ProcessorProgressEvent{
			Index: index, Total: len(plan.Postprocessors), Name: name, Status: "started",
		})

		// Restore the translated scene surface before the next processor
		// consumes it. This is deliberately before Process, never after the
		// merge: the final entities pass must be able to annotate the exact
		// text that will be persisted without being overwritten by an older
		// translated snapshot.
		reapplyTranslatedSceneText(&input)
		if sanitizer := procs[ProcessorNarrationSanitizer]; sanitizer != nil && name != ProcessorNarrationSanitizer {
			if err := runNarrationSanitizer(ctx, plan, sanitizer, &input, result); err != nil {
				return nil, err
			}
		}
		// The canonical Run owns timing whenever the worker has bound one to
		// the context. The no-run branch is retained only for legacy callers
		// and unit fixtures that execute the registry standalone.
		var (
			stageReport kernobs.StageReport
			ppResult    *PostProcessResult
			err         error
		)
		if kernobs.FromContext(ctx) != nil {
			stageReport, err = kernobs.MeasureStageReport(ctx, kernobs.StageName(name), func(stageCtx context.Context) error {
				processorCtx, cancel := context.WithTimeout(stageCtx, postprocessorOperationTimeout)
				defer cancel()
				var processErr error
				ppResult, processErr = proc.Process(processorCtx, plan, input)
				return processErr
			})
			// StageWithReport already returns the exact observation. The
			// assignment is intentionally projection-only: no second clock.
		} else {
			start := time.Now()
			processorCtx, cancel := context.WithTimeout(ctx, postprocessorOperationTimeout)
			ppResult, err = proc.Process(processorCtx, plan, input)
			cancel()
			legacyMs := time.Since(start).Milliseconds()
			stageReport = kernobs.StageReport{
				Name:       string(name),
				Status:     kernobs.StageStatusCompleted,
				DurationMs: legacyMs,
			}
			if err != nil {
				stageReport.Status = kernobs.StageStatusFailed
			}

		}
		adapter := r.canonicalTimingAdapter()
		if adapter == nil {
			adapter = &CanonicalTimingAdapter{VidRush: r.vidRushTimingMetrics()}
		}
		if projectionErr := adapter.ProjectStage(ctx, result, string(name), stageReport); projectionErr != nil {
			if r.log != nil {
				r.log.Warn("canonical timing projection failed",
					zap.String("name", string(name)),
					zap.Error(projectionErr))
			}
		}
		// Concurrency safety: a processor may return a shared/cached
		// PostProcessResult (common in stubs and caches). Clone before
		// mutating DurationMs or passing to merge so concurrent Run
		// calls cannot race on the same pointer.
		ppResult = clonePostProcessResult(ppResult)

		if err != nil {
			reportProcessorProgress(reporter, ProcessorProgressEvent{
				Index: index, Total: len(plan.Postprocessors), Name: name,
				Status: "failed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond, Err: err,
			})
			recordProcessorProgress(result, name, plan, input, job.StageFailed, plan.ID, err.Error())
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
			reportProcessorProgress(reporter, ProcessorProgressEvent{
				Index: index, Total: len(plan.Postprocessors), Name: name,
				Status: "failed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond,
				Err: fmt.Errorf("nil result"),
			})
			recordProcessorProgress(result, name, plan, input, job.StageFailed, plan.ID, "nil result")
			warn := fmt.Sprintf("postprocessor %q returned nil result", string(name))
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (nil result)")
			}
			continue
		}

		if ppResult.IsEmpty() {
			reportProcessorProgress(reporter, ProcessorProgressEvent{
				Index: index, Total: len(plan.Postprocessors), Name: name,
				Status: "failed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond,
				Err: fmt.Errorf("empty output"),
			})
			recordProcessorProgress(result, name, plan, input, job.StageFailed, plan.ID, "empty output")
			warn := fmt.Sprintf("postprocessor %q returned empty output", string(name))
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, string(name)+" (empty output)")
			}
			continue
		}

		recordProcessorProgress(result, name, plan, input, job.StageCompleted, plan.ID, "")
		reportProcessorProgress(reporter, ProcessorProgressEvent{
			Index: index, Total: len(plan.Postprocessors), Name: name,
			Status: "completed", Duration: time.Duration(stageReport.DurationMs) * time.Millisecond,
		})
		if policy == ProcessorRequired {
			requiredRequested++
			requiredSucceeded++
		}

		mergePostProcessResult(result, ppResult, &input)
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
	mergePostProcessResult(result, ppResult, input)
	return nil
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
		Version:           s.Version,
		Scenes:            make([]scriptpkg.SpecScene, len(s.Scenes)),
		VisualAssignments: append([]mediadomain.VisualAssignment(nil), s.VisualAssignments...),
	}
	for i, sc := range s.Scenes {
		out.Scenes[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			meta.Tags = append([]string(nil), sc.Metadata.Tags...)
			meta.Keywords = append([]string(nil), sc.Metadata.Keywords...)
			meta.Sources = append([]scriptpkg.SourceReference(nil), sc.Metadata.Sources...)
			out.Scenes[i].Metadata = &meta
		}
		if sc.Annotations != nil {
			ann := *sc.Annotations
			ann.ImportantPhrases = append([]scriptpkg.AnnotationSpan(nil), sc.Annotations.ImportantPhrases...)
			ann.ImportantWords = append([]scriptpkg.AnnotationSpan(nil), sc.Annotations.ImportantWords...)
			ann.Warnings = append([]string(nil), sc.Annotations.Warnings...)
			ann.PrimaryEntities = cloneAnnotatedEntities(sc.Annotations.PrimaryEntities)
			ann.SecondaryEntities = cloneAnnotatedEntities(sc.Annotations.SecondaryEntities)
			out.Scenes[i].Annotations = &ann
		}
		out.Scenes[i].Bindings = cloneSceneBindings(sc.Bindings)
	}
	return out
}

func cloneAnnotatedEntities(in []scriptpkg.AnnotatedEntity) []scriptpkg.AnnotatedEntity {
	if in == nil {
		return nil
	}
	out := make([]scriptpkg.AnnotatedEntity, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Mentions = append([]scriptpkg.AnnotationSpan(nil), in[i].Mentions...)
		if in[i].Image != nil {
			image := *in[i].Image
			out[i].Image = &image
		}
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
	if len(b.Clips) > 0 {
		out.Clips = make([]scriptpkg.ClipBinding, len(b.Clips))
		copy(out.Clips, b.Clips)
		// Keep the legacy alias pointing at the canonical first entry,
		// rather than cloning a second divergent binding.
		out.Clip = &out.Clips[0]
	} else if b.Clip != nil {
		c := *b.Clip
		out.Clip = &c
	}
	if b.Image != nil {
		img := *b.Image
		out.Image = &img
	}
	if b.Voiceover != nil {
		v := *b.Voiceover
		if b.Voiceover.Links != nil {
			v.Links = make(map[string]string, len(b.Voiceover.Links))
			for language, link := range b.Voiceover.Links {
				v.Links[language] = link
			}
		}
		if b.Voiceover.Timing != nil {
			v.Timing = make(map[string]scriptpkg.VoiceoverTimingBinding, len(b.Voiceover.Timing))
			for language, timing := range b.Voiceover.Timing {
				v.Timing[language] = timing
			}
		}
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

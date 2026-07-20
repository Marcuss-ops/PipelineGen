// Package usecase — generate_one_usecase.go is the canonical
// single-item script-generation orchestrator. It executes the
// unified pipeline for exactly one GenerationItemV2:
//
//	normalize → validate → resolve source → build plan → generate → postprocess → typed result
//
// This use case replaces the 3-way switch (clip-explicit /
// auto-search / text-only) in pipeline_run.go and the duplicated
// engine-call + postprocess logic across pipeline_handlers.go,
// catalog_job.go, curation_job.go, and media_curator.go.
//
// Split topology (July 2026):
//
//   - plan_resolution.go — GenerateOneUseCase struct + ctor + SetVoiceoverRouting +
//     buildResolutionContext
//   - engine_invoke.go   — logPhaseError + preConstructError + generateOnePreConstructError
//   - postprocessing.go  — reserved for future postprocess-phase extraction
//   - persistence.go     — buildGenerationResult
//   - generate_one_usecase.go (this file) — Execute orchestrator
//
// Dependencies:
//   - adapters.NormalizationConfig: config-driven defaults
//   - adapters.SourceRegistry: resolves source → ResolvedSource
//   - Engine: calls ollama for script text
//   - adapters.PostProcessorRegistry: runs postprocessors (entities, metadata,
//     voiceover, images, document, persistence)
//   - ports.VoiceoverGroupResolver + parent ID: optional. Set via
//     SetVoiceoverRouting so callers passing only `voiceover_group`
//     have their item.Output.VoiceoverFolderID populated BEFORE
//     BuildPlan runs (fix/voiceover-group-resolver, June 2026).
package usecase

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// Execute runs the full pipeline for one item and returns a typed
// GenerationResult. Progress is reported through the tracker when
// non-nil.
//
// godlike/07 typed-error gate (SCRIPT-T03-USECASE closure, July 2026):
// every `return nil, err` at the orchestrator boundary logs the
// diagnostic context (item_id, phase, error) via uc.log.Warn BEFORE
// returning the typed error. The typed error remains the propagation
// surface (handler reads it via errors.Is for HTTP status mapping)
// but the operator now has a log trail for every failure. This is
// the canonical "log+typed-propagate" pattern per godlike/07
// NO_FAKE_AVAILABILITY + TYPED_ERROR contract.
func (uc *GenerateOneUseCase) Execute(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*scriptpkg.GenerationResult, error) {
	if uc == nil {
		return nil, generateOnePreConstructError(nil, "uc_nil", scriptpkg.ErrGenerationFailed, fmt.Errorf("use case not constructed"))
	}
	if uc.engine == nil {
		return nil, uc.preConstructError("engine_nil", scriptpkg.ErrGenerationFailed, fmt.Errorf("engine not configured"))
	}

	startAll := time.Now()
	timings := scriptpkg.GenerationTimings{}

	// ── Phases 1-4: Prepare ─────────────────────────────────────────
	prepared, err := uc.preparer.Prepare(ctx, item, preset, tracker)
	if err != nil {
		return nil, err
	}
	item = prepared.Item
	plan := prepared.Plan
	resolved := prepared.ResolvedSource
	timings.SourceResolveMs = prepared.SourceResolveMs
	timings.PlanBuildMs = prepared.PlanBuildMs

	// ── Phase 5: Generate script ────────────────────────────────────
	tracker.PhaseGenerateStart()
	engineStart := time.Now()
	engineResult, engineErr := uc.engine.Generate(ctx, &plan)
	if engineErr != nil {
		genErr := &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "engine",
			Inner:  fmt.Errorf("ollama generation failed: %w", engineErr),
		}
		return nil, uc.logPhaseError(item, "engine", scriptpkg.ErrGenerationFailed, genErr, tracker)
	}
	timings.EngineMs = time.Since(engineStart).Milliseconds()
	tracker.PhaseGenerateDone()
	tracker.TrackEvent("script.generated", "Script text generated", map[string]any{
		"item_id":      item.ID,
		"word_count":   engineResult.WordCount,
		"model":        engineResult.Model,
		"cache_status": engineResult.CacheStatus,
	})
	if len(engineResult.Output.SpecScene.Scenes) > 0 {
		tracker.TrackEvent("scenes.created", "Scenes created from generated script", map[string]any{
			"item_id":     item.ID,
			"scene_count": len(engineResult.Output.SpecScene.Scenes),
		})
	}

	// ── Phase 6: Postprocess ────────────────────────────────────────
	timings.PostprocessMs = make(map[string]int64)
	var postResult *adapters.PipelineResult
	// Provenance block (without doc_id/doc_link) is built before
	// postprocessing so the document processor can embed it and fill
	// the document identifiers after creating/updating the Google Doc.
	modeInfo := provisionalModeInfo(plan, engineResult)
	provenance := buildProvenance(plan, engineResult, modeInfo)

	if uc.ppReg != nil {
		for _, pp := range plan.Postprocessors {
			tracker.PhasePostprocess(pp)
		}
		procInput := adapters.ProcessInput{
			Text:        engineResult.Output.Text,
			WordCount:   engineResult.WordCount,
			SpecScene:   engineResult.Output.SpecScene,
			ModelUsed:   engineResult.Model,
			CacheStatus: engineResult.CacheStatus,
			SourceTrace: engineResult.ClipEvidence,
			Provenance:  provenance,
		}
		var ppErr error
		postResult, ppErr = uc.ppReg.Run(ctx, &plan, procInput)
		if ppErr != nil {
			ppErrStruct := &scriptpkg.PostprocessError{
				ItemID:    item.ID,
				Processor: "registry",
				Inner:     ppErr,
			}
			return nil, uc.logPhaseError(item, "postprocess", scriptpkg.ErrPostprocessFailed, ppErrStruct, tracker)
		}
		if postResult != nil && len(postResult.StageDurations) > 0 {
			timings.PostprocessMs = maps.Clone(postResult.StageDurations)
		}
		if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
			tracker.TrackEvent("clips.bound", "Clip bindings applied", map[string]any{
				"item_id":    item.ID,
				"clip_count": len(plan.ClipEvidence.AcceptedClipIDs),
			})
		}
	}

	// ── Phase 7: Build result ───────────────────────────────────────
	result := buildGenerationResult(item, plan, engineResult, postResult, timings)

	// ── Phase 7b: Enforce clip-native contract ─────────────────────
	// Strict clip-native sources must produce exactly one scene per
	// accepted clip and bind every accepted clip. Explicit fallback
	// mode reports SUCCEEDED_WITH_WARNINGS instead of failing.
	if err := enforceClipNativeContract(result, item, plan, engineResult, postResult); err != nil {
		return nil, uc.logPhaseError(item, "clip_native", scriptpkg.ErrClipNativePlanningFailed, err, tracker)
	}

	// ── Phase 8: Surface provenance ─────────────────────────────────
	// The provenance pointer was populated by the document processor
	// with doc_id/doc_link (and possibly refined mode fields). Surface
	// it on the result before the quality gate so a failing gate still
	// returns the provenance block.
	result.Provenance = provenance

	// ── Phase 9: Editorial quality gate ────────────────────────────
	// The engine generates the source-language script first. Translation
	// is a postprocessor and intentionally replaces the response text with
	// the requested target-language content. Evaluate editorial language,
	// source coverage, and word budget against the engine output, otherwise
	// a valid EN→ES translation is incorrectly compared to plan.Language=EN.
	// The final result still contains the translated text produced by the
	// translation processor.
	qualityInput := *result
	qualityInput.Output = result.Output
	qualityInput.Output.Text = engineResult.Output.Text
	qualityInput.Output.WordCount = engineResult.Output.WordCount
	quality, qErr := evaluateQualityGate(&qualityInput, item, plan)
	if quality != nil {
		result.Quality = quality
	}
	if quality != nil {
		tracker.TrackEvent("quality.checked", "Editorial quality gate checked", map[string]any{
			"item_id":                item.ID,
			"passed":                 quality.Passed,
			"source_text_coverage":   quality.SourceTextCoverage,
			"clip_evidence_coverage": quality.ClipEvidenceCoverage,
			"unsupported_claims":     quality.UnsupportedClaims,
			"actual_words":           quality.ActualWords,
			"target_words":           quality.TargetWords,
		})
	}
	if qErr != nil {
		if item.ScriptParams.SkipQualityGate {
			tracker.TrackEvent("quality.skipped", "Editorial quality gate failure ignored by request", map[string]any{
				"item_id": item.ID,
				"error":   qErr.Error(),
			})
		} else {
			result.Status = "FAILED_QUALITY_GATE"
			return result, uc.logPhaseError(item, "quality_gate", scriptpkg.ErrQualityGateFailed, qErr, tracker)
		}
	}

	timings.TotalMs = time.Since(startAll).Milliseconds()
	result.Timings = timings
	if result.Artifacts.Document != nil && (result.Artifacts.Document.DocID != "" || result.Artifacts.Document.DocLink != "") {
		tracker.TrackEvent("document.created", "Output document created", map[string]any{
			"item_id":  item.ID,
			"doc_id":   result.Artifacts.Document.DocID,
			"doc_link": result.Artifacts.Document.DocLink,
		})
	}

	if resolved != nil && len(resolved.SearchResults) > 0 {
		result.Source.SearchResults = resolved.SearchResults
	}

	tracker.PhaseComplete()

	if uc.log != nil {
		uc.log.Info("generate-one: completed",
			zap.String("item_id", item.ID),
			zap.String("title", plan.Title),
			zap.Int("word_count", result.Output.WordCount),
			zap.String("cache_status", result.Cache.Status),
			zap.Int64("total_ms", timings.TotalMs))
	}

	// Log source text metrics once at completion. The raw source text
	// is never logged; only hash, length, token estimate and an
	// optional preview are emitted.
	if uc.log != nil {
		uc.log.Info("generate-one: source text metrics",
			zap.String("item_id", item.ID),
			zap.Any("source_text", SourceTextLogFields(plan.SourceText, uc.cfg)))
	}

	tracker.TrackEvent("job.completed", "Script generation completed", map[string]any{
		"item_id":    item.ID,
		"status":     "success",
		"total_ms":   timings.TotalMs,
		"word_count": result.Output.WordCount,
	})

	return result, nil
}

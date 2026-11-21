// Package scriptgeneration — scene-text generation path selection, batch/streaming
// dispatch and the Phrase Impact enrichment pass.
// Verbatim sibling split of runner_phase_script.go per the 600-LOC split wave;
// behaviour unchanged (same package, same types, same call sites).
package scriptgeneration

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// ── Scene-text generation path selection + dispatch ────────────────
type sceneTextGenerationPath struct {
	streamable                   bool
	topologyNeedsMaterialization bool
	ready                        *sceneReadyCoordinator
}

func (r *Runner) prepareSceneTextGeneration(ctx context.Context, runID string, req GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext) sceneTextGenerationPath {
	path := sceneTextGenerationPath{
		streamable:                   SceneStreamingEligibility(req),
		topologyNeedsMaterialization: req.ScriptParams.SegmentWords > 0 && !req.ScriptParams.SingleScene && len(req.ScriptParams.Segments) == 0,
	}
	// ORCH-07: stock bindings no longer force the batch path. The streaming
	// emit applies the binding to each scene before it is committed and
	// submitted to the SceneTextReady coordinator, so the coordinator can
	// never observe a stock-bound scene with Stock == nil.
	if len(req.MediaPlan.Extraction.ImportantPhrases) > 0 && !importantPhraseHintOwnersAvailable(req) {
		path.streamable = false
		path.topologyNeedsMaterialization = true
	}
	// Fixed intro/outro sections are NOT a streaming gate: applyFixedSections
	// injects them as pure post-processing AFTER ready.wait joins the per-scene
	// downstream (translation/TTS/NLP), so generated scenes stream unchanged and
	// the fixed sections never reach the scene-ready coordinator.
	if req.ScriptParams.SourceTextVerbatim {
		return path
	}
	streamEligible := !path.topologyNeedsMaterialization && (req.Source.Type != SourceClips || path.streamable)
	if streamEligible {
		if _, ok := r.textGen.(SceneTextStreamer); ok {
			path.ready = newSceneReadyCoordinator(ctx, r, runID, req, routing, exec)
		}
	}
	return path
}

func (r *Runner) generateSceneText(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, path sceneTextGenerationPath) ([]Scene, scriptpkg.SourceTrace, bool, error) {
	if req.ScriptParams.SourceTextVerbatim {
		scenes, err := materializeVerbatimSourceTextScenes(req)
		return scenes, scriptpkg.SourceTrace{}, false, err
	}

	streamEligible := !path.topologyNeedsMaterialization && (req.Source.Type != SourceClips || path.streamable)
	if streamer, ok := r.textGen.(SceneTextTraceStreamer); ok && streamEligible {
		scenes, trace, err := r.generateSceneTextStreamingWithTrace(ctx, runID, req, exec, streamer, path.ready)
		return scenes, trace, true, err
	}
	if streamer, ok := r.textGen.(SceneTextStreamer); ok && streamEligible {
		scenes, err := r.generateSceneTextStreaming(ctx, runID, req, exec, streamer, path.ready)
		return scenes, scriptpkg.SourceTrace{}, true, err
	}
	// I2 (bounded in-phase retry): the batch branches publish nothing before
	// they return, so replaying them is side-effect free. ONLY typed-transient
	// infrastructure errors (transport failure, 502/503/504 — marked by the
	// Ollama client) get one extra attempt; validation, certification and
	// context-cancellation errors fail closed on the first attempt. Streaming
	// branches are excluded: they commit incrementally and replaying them
	// could duplicate downstream work.
	if traced, ok := r.textGen.(SceneTextTraceGenerator); ok {
		out, err := retry.DoWithValue(ctx, func() (sceneTextBatchResult, error) {
			scenes, trace, err := traced.GenerateSceneTextWithTrace(ctx, req)
			return sceneTextBatchResult{scenes: scenes, trace: trace}, err
		}, sceneTextRetryOptions())
		return out.scenes, out.trace, false, err
	}
	out, err := retry.DoWithValue(ctx, func() (sceneTextBatchResult, error) {
		scenes, err := r.textGen.GenerateSceneText(ctx, req)
		return sceneTextBatchResult{scenes: scenes}, err
	}, sceneTextRetryOptions())
	return out.scenes, out.trace, false, err
}

// sceneTextBatchResult carries the batch generate tuple through the retry engine.
type sceneTextBatchResult struct {
	scenes []Scene
	trace  scriptpkg.SourceTrace
}

// phraseImpactEnrichment is the value-only Phrase Impact product computed
// off the critical path (P1-C). It never mutates the result; the caller
// applies the returned fields serially after the parallel join.
type phraseImpactEnrichment struct {
	summary         string
	bullets         []string
	heavy           []scriptpkg.ImportantSentence
	chapterManifest *scriptpkg.ChapterManifest
	highlights      []scriptpkg.SceneHighlight
	editorial       *scriptpkg.EditorialManifest
	timings         []PhraseImpactTimings
	// Semantic planner views from the same worker batch. They are optional
	// products: a worker without the consolidation projection leaves them nil.
	consolidation  *SemanticVisualConsolidation
	productionPlan *SemanticProductionPlan
	searchFeedback *SemanticSearchFeedback
}

// adoptSemanticPlannerView records the consolidated view, its executable plan
// and the planner-owned feedback. A planning error is logged and withholds the
// plan instead of failing the run: the editorial products already computed from
// the same batch stay valid.
func (r *Runner) adoptSemanticPlannerView(out *phraseImpactEnrichment, bundles map[string]*SemanticCandidateBundle, req GenerateRequest) {
	if len(bundles) == 0 {
		return
	}
	config := DefaultSemanticProductionPlanConfig()
	if req.MaxImageOverlays > 0 {
		config.MaxMediaUnits = req.MaxImageOverlays
	}
	if req.MediaPlan.Planner.MaxImagesPerScene > 0 {
		config.MaxMediaUnitsPerScene = req.MediaPlan.Planner.MaxImagesPerScene
	}
	consolidation, plan, feedback, err := PlanScriptProduction(bundles, config)
	out.consolidation = consolidation
	if err != nil {
		if r.log != nil {
			r.log.Warn("semantic production plan withheld", zap.Error(err))
		}
		return
	}
	out.productionPlan = plan
	callerSearchesAdded := ApplyCallerImageSearches(plan, req.MediaPlan.Searches, req.MediaPlan.Assignments)
	if len(req.MediaPlan.Searches) > 0 && callerSearchesAdded == 0 && r.log != nil {
		r.log.Warn("caller image searches did not match a planned media scene",
			zap.Int("searches", len(req.MediaPlan.Searches)))
	} else if callerSearchesAdded > 0 && r.log != nil {
		r.log.Info("caller image searches added to semantic production plan",
			zap.Int("searches", len(req.MediaPlan.Searches)),
			zap.Int("added", callerSearchesAdded),
			zap.Int("planned_queries", plan.Metrics.PlannedQueries))
	}
	if len(req.MediaPlan.Searches) > 0 {
		out.searchFeedback = plan.SearchFeedback()
	} else {
		out.searchFeedback = feedback
	}
}

// maxVerbatimEditorialTranscriptRunes bounds the semantic/editorial batch for
// an authored verbatim script. The batch is needed for the automatic editorial
// titles/bullets, but a long narration can exceed the local embedder's request
// window; above this bound the historical "preserve only" behaviour is kept.
const maxVerbatimEditorialTranscriptRunes = 12000

// shouldComputeEditorialEnrichment decides whether the optional semantic batch
// runs. A verbatim source is never rewritten, but it still needs the editorial
// projection when the caller asked for automatic titles/bullets. The old guard
// skipped the batch unconditionally, so a verbatim payload with editorial
// overlays enabled reached the planner without an `editorial` section and the
// whole plan failed closed.
func shouldComputeEditorialEnrichment(req GenerateRequest, transcript string) bool {
	if !req.ScriptParams.SourceTextVerbatim {
		return true
	}
	if req.DisableEditorialOverlays {
		return false
	}
	return utf8.RuneCountInString(transcript) <= maxVerbatimEditorialTranscriptRunes
}

func (r *Runner) computePhraseImpactEnrichment(ctx context.Context, transcript string, scenes []Scene, req GenerateRequest) (*phraseImpactEnrichment, error) {
	// A verbatim source is already authored and must not be rewritten. The
	// optional semantic/editorial batch still runs for a short verbatim script
	// when automatic editorial overlays are enabled: without it the planner has
	// no scene titles/bullets and the plan fails closed. Long narration keeps
	// the historical skip so it cannot exceed the local embedder window.
	if !shouldComputeEditorialEnrichment(req, transcript) {
		return &phraseImpactEnrichment{}, nil
	}
	chapterScenes, chapterTopics := make([]string, 0, len(scenes)), make([]string, 0, len(scenes))
	for _, scene := range scenes {
		if !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		text := strings.TrimSpace(scene.Text[req.SourceLanguage])
		if text == "" {
			continue
		}
		chapterSceneIndex := len(chapterScenes)
		chapterScenes = append(chapterScenes, text)
		topic := scene.ID
		for _, spec := range req.ScriptParams.Segments {
			if spec.ID != "" && spec.ID == scene.ID && strings.TrimSpace(spec.Topic) != "" {
				topic = spec.Topic
				break
			}
		}
		if topic == scene.ID && chapterSceneIndex < len(req.ScriptParams.Segments) && strings.TrimSpace(req.ScriptParams.Segments[chapterSceneIndex].Topic) != "" {
			topic = req.ScriptParams.Segments[chapterSceneIndex].Topic
		}
		chapterTopics = append(chapterTopics, topic)
	}
	if strings.Join(chapterScenes, "\n\n") != transcript {
		chapterScenes, chapterTopics = nil, nil
	}
	sceneInputs := buildSceneAnalysisInputs(transcript, scenes, req)
	out := &phraseImpactEnrichment{}
	language := string(req.SourceLanguage)
	if batch, ok := r.sceneSemanticAnalyzer.(SceneSemanticEditorialBatchAnalyzer); ok {
		if len(sceneInputs) == 0 {
			return out, nil
		}
		semanticBundles, impactRes, err := batch.AnalyzeSemanticEditorial(ctx, transcript, language, sceneInputs)
		if err == nil {
			r.adoptSemanticPlannerView(out, semanticBundles, req)
			out.summary = impactRes.Summary
			out.bullets = impactRes.BulletPoints
			out.heavy = impactRes.HeavySentences
			if impactRes.ChapterManifest.SchemaVersion != "" {
				manifest := impactRes.ChapterManifest
				out.chapterManifest = &manifest
			}
			chartsEnabled := req.EnableCharts && !req.DisableChartOverlays
			impactRes.SceneHighlights = r.mergeSemanticCharts(sceneInputs, impactRes.SceneHighlights, semanticBundles, chartsEnabled)
			impactRes.SceneHighlights = mergeSemanticMicroSummaries(sceneInputs, impactRes.SceneHighlights, semanticBundles)
			if !chartsEnabled {
				for i := range impactRes.SceneHighlights {
					impactRes.SceneHighlights[i].ChartValues = nil
				}
			}
			out.highlights = impactRes.SceneHighlights
			out.editorial = buildEditorialManifest(req, sceneInputs, impactRes)
			out.timings = append(out.timings, impactRes.Timings)
			return out, nil
		}
		if r.log != nil {
			r.log.Warn("combined Rust semantic/editorial batch failed", zap.Error(err))
		}
		// Production uses this unified adapter; a failure keeps the optional
		// editorial fields empty instead of launching a second analysis path.
		return out, nil
	}
	return out, nil
}

// sceneTextRetryOptions allows exactly one extra attempt, only for
// typed-transient infrastructure errors. A full generate costs ~100s, so a
// single retry is cheaper than a dead job plus a whole-job retry; more than
// one would risk amplifying an overloaded Ollama into a retry storm.
func sceneTextRetryOptions() retry.Options {
	return retry.Options{
		MaxAttempts:    2,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     8 * time.Second,
		BackoffFactor:  2.0,
		DisableJitter:  true,
		IsRetryable:    retry.IsTransient,
	}
}

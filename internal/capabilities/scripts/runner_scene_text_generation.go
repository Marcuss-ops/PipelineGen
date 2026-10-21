// Package scriptgeneration — scene-text generation path selection, batch/streaming
// dispatch and the Phrase Impact enrichment pass.
// Verbatim sibling split of runner_phase_script.go per the 600-LOC split wave;
// behaviour unchanged (same package, same types, same call sites).
package scriptgeneration

import (
	"context"
	"strings"
	"time"

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
	if len(req.MediaPlan.Extraction.ImportantPhrases) > 0 && !importantPhraseHintOwnersAvailable(req) {
		path.streamable = false
		path.topologyNeedsMaterialization = true
	}
	if req.Intro != nil || req.Outro != nil {
		path.streamable = false
		path.topologyNeedsMaterialization = true
	}
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
}

func (r *Runner) computePhraseImpactEnrichment(ctx context.Context, transcript string, scenes []Scene, req GenerateRequest) (*phraseImpactEnrichment, error) {
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
			out.summary = impactRes.Summary
			out.bullets = impactRes.BulletPoints
			out.heavy = impactRes.HeavySentences
			if impactRes.ChapterManifest.SchemaVersion != "" {
				manifest := impactRes.ChapterManifest
				out.chapterManifest = &manifest
			}
			chartsEnabled := req.EnableCharts && !req.DisableChartOverlays
			impactRes.SceneHighlights = r.mergeSemanticCharts(sceneInputs, impactRes.SceneHighlights, semanticBundles, chartsEnabled)
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
		// Production uses this unified adapter. Do not silently launch the
		// retired Phrase Impact worker as a second analysis path on failure.
		return out, nil
	}
	if r.phraseImpactAnalyzer == nil {
		return out, nil
	}
	var rawImpact interface{} = r.phraseImpactAnalyzer
	if sceneAnalyzer, ok := rawImpact.(SceneHighlightAnalyzer); ok && len(sceneInputs) > 0 {
		var semanticBundles map[string]*SemanticCandidateBundle
		var semanticReady bool
		var semanticBullets []scriptpkg.SemanticBulletSpan
		if r.sceneSemanticAnalyzer != nil {
			semanticBundles, semanticReady = r.analyzeSemanticBundles(ctx, language, sceneInputs)
			if semanticReady {
				semanticBullets, semanticReady = semanticBulletSpans(transcript, sceneInputs, semanticBundles)
			}
		}
		var impactRes scriptpkg.PhraseImpactResult
		var err error
		if candidateAnalyzer, ok := rawImpact.(SemanticBulletCandidateAnalyzer); ok && semanticReady {
			impactRes, err = candidateAnalyzer.AnalyzeScenesWithSemanticBullets(ctx, transcript, language, sceneInputs, semanticBullets)
		} else {
			impactRes, err = sceneAnalyzer.AnalyzeScenes(ctx, transcript, language, sceneInputs)
		}
		if err != nil {
			return nil, err
		}
		out.summary = impactRes.Summary
		out.bullets = impactRes.BulletPoints
		out.heavy = impactRes.HeavySentences
		if impactRes.ChapterManifest.SchemaVersion != "" {
			manifest := impactRes.ChapterManifest
			out.chapterManifest = &manifest
		}
		chartsEnabled := req.EnableCharts && !req.DisableChartOverlays
		if semanticReady {
			impactRes.SceneHighlights = r.mergeSemanticCharts(sceneInputs, impactRes.SceneHighlights, semanticBundles, chartsEnabled)
		}
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
	legacy, err := r.phraseImpactAnalyzer.AnalyzeWithContext(ctx, transcript, language, chapterScenes, chapterTopics)
	if err != nil {
		return nil, err
	}
	out.summary = legacy.Summary
	out.bullets = legacy.BulletPoints
	out.heavy = legacy.HeavySentences
	if legacy.ChapterManifest.SchemaVersion != "" {
		manifest := legacy.ChapterManifest
		out.chapterManifest = &manifest
	}
	out.timings = append(out.timings, legacy.Timings)
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

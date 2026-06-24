// Package scripts — pipeline_usecase is the orchestrator for the
// unified clip-source script-generation job (the heavy
// HandleClipScriptGenerateJob entry point).
//
// Wave 14 problem #4 (June 2026): previously ~280 LOC lived inline
// in api/script/handler_jobs.go::ScriptFlowHandler.HandleClipScriptGenerateJob:
//   - semaphore acquire/release
//   - payload decode
//   - scenes/doc services construction (two inline NewXxxService
//     calls)
//   - 3-way switch (clip-explicit / auto-search / text-only)
//   - prewarm goroutine launch
//   - pipeline.Run invocation
//   - buildFinalResult shaping
//   - return contract on the happy path
//
// Each of these belonged to the application layer:
//   - transport (handler) only reads the job payload, signals
//     sem/prewarm lifecycle, and calls the use case;
//   - orchestration (PipelineUseCase) decodes the payload, dispatches
//     to one of the three paths, calls the existing Pipeline.Run, and
//     shapes the response map.
//
// The use case owns:
//   - payload decoding (scriptpkg.DecodeGeneratePayload)
//   - 3-way switch with explicit failure messages per branch
//   - phase 2-4 invocation via the existing *Pipeline (pre-built
//     by composition with scenes + docs + post-gen callback wired)
//   - final-result-map shaping (buildFinalResult)
//   - HandleJob wrapper that acquires sem + kicks prewarm
//   - RegisterJobs hook so the handler no longer owns registration
//
// The use case does NOT own:
//   - the semaphore acquire/release (delegates to SemaphoreUseCase via
//     HandleJob)
//   - the prewarm goroutine (delegates to PrewarmUseCase via
//     HandleJob)
//   - HTTP transport shape, status codes, gin Error/OK helpers
//     (handler responsibility)
//   - the script-engine choice of model / language / tone (engine's
//     WriteScript's responsibility)
package scripts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ErrInvalidPayload is the sentinel for "the JSON payload the worker
// forwarded to us could not be decoded". Maps to a job-system
// permanent failure (no retry).
var ErrInvalidPayload = errors.New("pipeline: invalid job payload")

// ErrBrokerNotSatisfied is the sentinel for "the caller supplied a
// jobsSvc value that does not implement the canonical Broker port".
// Returned from RegisterJobs when the type-assertion to Broker fails
// on a non-nil input. Maps to a job-system permanent failure (no retry):
// the composition root wired the wrong shape, retrying the same input
// will not recover. Replaces the silent-skip behavior of the prior
// structural-interface widening (AGENT-2 cycle 5), giving first-
// integration-test feedback instead of a missing-handler surprise at
// first job dispatch.
var ErrBrokerNotSatisfied = errors.New("pipeline: jobsSvc does not satisfy Broker port")

// ErrClipPipelineUnavailable is the sentinel for "the request gave
// explicit ClipIDs but no ClipSourceBuilder is wired". Maps to a
// typed 503-class error in the handler.
var ErrClipPipelineUnavailable = errors.New("pipeline: clip pipeline unavailable")

// ErrAutoSearchUnavailable is the sentinel for "the request asked
// for num_clips but no MediaCurator is wired".
var ErrAutoSearchUnavailable = errors.New("pipeline: auto-search pipeline unavailable")

// ErrPipelineGenerationFailed is the generic wrapper for any failure
// inside Run. The inner error is accessible via errors.Unwrap.
var ErrPipelineGenerationFailed = errors.New("pipeline: generation failed")

// PipelineUseCase orchestrates the unified clip-source job. The
// *Pipeline it holds is pre-built by composition (it already carries
// scenes-svc, docs-svc, postGen callback, and resolve-folder — so
// Reuse the existing application-layer infrastructure unchanged).
type PipelineUseCase struct {
	log          *zap.Logger
	engine       *Engine
	cfg          *configShim
	clipBuilder  *ClipSourceBuilder
	mediaCurator *MediaCurator
	semUC        *SemaphoreUseCase
	prewarmUC    *PrewarmUseCase
	pipeline     *Pipeline
}

// configShim wraps *config.Config so a nil cfg doesn't break the
// text-only path's defaults (previous handler's `if h.cfg != nil`
// guard). Avoids an `internal/infrastructure/config` import in the
// use-case struct field while still letting the ctor receive a cfg.
type configShim struct {
	minWordFloor int
	ollamaModel  string
}

func newConfigShim(minWordFloor int, ollamaModel string) *configShim {
	return &configShim{minWordFloor: minWordFloor, ollamaModel: ollamaModel}
}

// NewPipelineUseCase wires the orchestrator. The constructor refuses
// to build if engine or pipeline is nil — those are the canonical
// components for the happy path. Other args (semUC, prewarmUC,
// clipBuilder, mediaCurator) may be nil; their absence is surfaced
// as a typed error at the dispatch step or as a no-op for prewarm.
//
// Composition root builds:
//   the *Pipeline via NewPipeline(... scenesUC.Build(...) ...
//      documentsUC.DocumentsService() ... postGenClosure ...).
// This use-case receives that pre-built pointer.
func NewPipelineUseCase(
	log *zap.Logger,
	engine *Engine,
	minWordFloor int,
	ollamaModel string,
	clipBuilder *ClipSourceBuilder,
	mediaCurator *MediaCurator,
	semUC *SemaphoreUseCase,
	prewarmUC *PrewarmUseCase,
	pipeline *Pipeline,
) (*PipelineUseCase, error) {
	if engine == nil {
		return nil, fmt.Errorf("%w: engine is required", ErrPipelineGenerationFailed)
	}
	if pipeline == nil {
		return nil, fmt.Errorf("%w: pipeline is required", ErrPipelineGenerationFailed)
	}
	return &PipelineUseCase{
		log:          log,
		engine:       engine,
		cfg:          newConfigShim(minWordFloor, ollamaModel),
		clipBuilder:  clipBuilder,
		mediaCurator: mediaCurator,
		semUC:        semUC,
		prewarmUC:    prewarmUC,
		pipeline:     pipeline,
	}, nil
}

// Run executes the full clip-source job. The caller (HandleJob or a
// test) has already acquired a semaphore slot via SemaphoreUseCase
// and started prewarm via PrewarmUseCase; this method owns phase 1
// (path dispatch) + phase 2-4 (pipelines) + result shaping.
//
// Returns the result map (same shape as the old buildFinalResult
// output) on success, or a typed error otherwise.
func (pu *PipelineUseCase) Run(
	ctx context.Context,
	j *job.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if pu == nil {
		return nil, fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}

	genPayload, err := scriptpkg.DecodeGeneratePayload(j.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: decode payload: %w", ErrInvalidPayload, err)
	}
	spec := &genPayload.Spec

	if pu.log != nil {
		pu.log.Info("pipeline_dispatch_decided",
			zap.String("job_id", j.ID),
			zap.Int("clip_ids", len(spec.ClipIDs)),
			zap.Int("num_clips", spec.NumClips),
			zap.Bool("extract_entities", spec.ExtractEntities),
			zap.Bool("generate_scene_images", spec.GenerateSceneImages),
			zap.Bool("generate_voiceover", spec.GenerateVoiceover),
			zap.Int("sentences_per_image", spec.SentencesPerImage),
			zap.Int("images_per_scene", spec.ImagesPerScene),
			zap.String("language", spec.Language),
			zap.String("style", spec.Style))
	}

	startAll := time.Now()

	var pathResult *ClipSourcePathResult

	pathStart := time.Now()
	switch {
	case len(spec.ClipIDs) > 0:
		if pu.clipBuilder == nil {
			return nil, fmt.Errorf("%w: %d clip_ids provided but clipSourceBuilder is not initialized",
				ErrClipPipelineUnavailable, len(spec.ClipIDs))
		}
		pathResult, err = pu.handleClipPathExplicit(ctx, spec, tools)
	case spec.NumClips > 0:
		if pu.mediaCurator == nil {
			return nil, fmt.Errorf("%w: num_clips=%d requested but mediaCurator is not initialized",
				ErrAutoSearchUnavailable, spec.NumClips)
		}
		pathResult, err = pu.handleClipPathAutoSearch(ctx, spec, tools)
	default:
		pathResult, err = pu.handleClipPathTextOnly(ctx, spec, tools)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: path: %w", ErrPipelineGenerationFailed, err)
	}
	if pu.log != nil {
		pu.log.Info("script_generation_completed",
			zap.String("job_id", j.ID),
			zap.Int("script_chars", len(pathResult.WriteResult.Script)),
			zap.Int("word_count", pathResult.WriteResult.WordCount),
			zap.String("cache_status", pathResult.WriteResult.CacheStatus),
			zap.Int64("path_ms", time.Since(pathStart).Milliseconds()))
	}

	pipelineResult, pipeErr := pu.pipeline.Run(ctx, spec, pathResult.WriteResult.Script, tools)
	if pipeErr != nil {
		return nil, fmt.Errorf("%w: pipeline: %w", ErrPipelineGenerationFailed, pipeErr)
	}

	totalDurMs := time.Since(startAll).Milliseconds()

	if pu.log != nil {
		pu.log.Info("pipeline_completed",
			zap.String("job_id", j.ID),
			zap.Int64("total_ms", totalDurMs),
			zap.Int("scenes", len(pipelineResult.Scenes)),
			zap.Int("voiceovers", len(pipelineResult.Voiceovers)),
			zap.Bool("has_doc", pipelineResult.DocLink != ""))
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(100, "Generation completed")
	}

	var scriptInsights ScriptInsights
	if ins, ok := pipelineResult.Insights.(ScriptInsights); ok {
		scriptInsights = ins
	}
	scriptMeta := make([]VideoMetadata, len(pipelineResult.VideoMetadata))
	for i, m := range pipelineResult.VideoMetadata {
		scriptMeta[i] = VideoMetadata{
			Language:    m.Language,
			Title:       m.Title,
			Description: m.Description,
			Tags:        m.Tags,
		}
	}

	return pu.buildFinalResult(spec, pathResult,
		pipelineResult.EntitiesJSON,
		scriptInsights,
		scriptMeta,
		pipelineResult.DocLink,
		pipelineResult.DocID,
		pipelineResult.Scenes,
		pipelineResult.Voiceovers,
		totalDurMs), nil
}

// RegisterJobs wires the pipeline job handler into the canonical
// jobs service. Lives on the use case so the handler no longer
// owns job-registration logic (handler is purely transport).
//
// AGENT-2 (June 2026): the canonical Broker port is declared in
// `ports.go` of this package; it uses the same `appjobs.HandlerFunc`
// shape that `*jobs.Service.RegisterHandler` exposes, so the type
// assertion is structural and matches without a cast. The parameter
// stays `interface{}` to preserve the upstream convention that
// composition root can hand in either `*job.Service` or `*jobs.Service`
// without import gymnastics; the assert-then-error path promotes
// the prior silent-skip behavior to a typed `ErrBrokerNotSatisfied`
// so a wrong-shape wiring is detected at first integration test
// rather than as a missing-handler at first job dispatch.
//
// Producer-side compile assertion (see ports.go): `var _ Broker = (*appjobs.Service)(nil)`
// guards signature drift at build time.
func (pu *PipelineUseCase) RegisterJobs(jobsSvc interface{}) error {
	if pu == nil {
		return fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}
	if jobsSvc == nil {
		return nil
	}
	broker, ok := jobsSvc.(Broker)
	if !ok {
		if pu.log != nil {
			pu.log.Error("pipeline use case: jobsSvc did not satisfy Broker port — composition root wiring error",
				zap.String("concrete_type", fmt.Sprintf("%T", jobsSvc)))
		}
		return fmt.Errorf("%w: got %T", ErrBrokerNotSatisfied, jobsSvc)
	}
	if err := broker.RegisterHandler(job.TypeClipScriptGenerate, pu.HandleJob); err != nil {
		return fmt.Errorf("pipeline: register handler: %w", err)
	}
	if pu.log != nil {
		pu.log.Info("registered script.generate_from_clips job handler")
	}
	return nil
}

// HandleJob is the canonical job-system entry point. Acquires the
// sem slot, kicks off the prewarm goroutine (if applicable), and
// delegates to Run. The worker receives the typed error chain so
// the job system can classify failures (permanent vs retryable).
func (pu *PipelineUseCase) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if pu == nil {
		return nil, fmt.Errorf("%w: not constructed", ErrPipelineGenerationFailed)
	}
	if pu.log != nil {
		pu.log.Info("handling unified script generation job", zap.String("job_id", j.ID))
	}

	if pu.semUC != nil {
		release, err := pu.semUC.Acquire(ctx, j.ID)
		if err != nil {
			return nil, err
		}
		defer release()
	}

	// Pre-parse just enough of the payload to know whether to prewarm;
	// keeps the goroutine decision fully local to this handler. If
	// decode fails, the actual Run will surface ErrInvalidPayload — we
	// just skip the prewarm on the failure path.
	shouldPrewarm := false
	if pp, perr := scriptpkg.DecodeGeneratePayload(j.Payload); perr == nil && pp != nil {
		spec := &pp.Spec
		shouldPrewarm = ShouldStart(spec.GenerateSceneImages, len(spec.ClipIDs), spec.NumClips)
	}
	if pu.prewarmUC != nil {
		_ = pu.prewarmUC.Start(ctx, j.ID, shouldPrewarm)
	}

	return pu.Run(ctx, j, tools)
}

// handleClipPathExplicit — moved inline 1:1 from the previous handler.
func (pu *PipelineUseCase) handleClipPathExplicit(ctx context.Context, payload *scriptpkg.GenerationSpec, tools *appjobs.JobTools) (*ClipSourcePathResult, error) {
	if pu.log != nil {
		pu.log.Info("clip-aware path: generating script from explicit clip IDs",
			zap.Int("clip_ids", len(payload.ClipIDs)))
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(10, "Loading clips and building evidence cards")
	}

	clipSvc := pu.clipBuilder
	opts := &ClipGenerationOptions{
		Language:          payload.Language,
		Tone:              payload.Tone,
		Title:             payload.Title,
		Model:             payload.Model,
		TargetWords:       payload.TargetWords,
		SourceText:        payload.SourceText,
		TranscriptPolicy:  payload.TranscriptPolicy,
		OrderingStrategy:  payload.OrderingStrategy,
		StyleInstructions: payload.Guidelines,
	}
	if payload.MinQualityScore != nil {
		opts.MinQualityScore = *payload.MinQualityScore
	}
	if payload.MinTranscriptWords != nil {
		opts.MinTranscriptWords = *payload.MinTranscriptWords
	}
	pack, plan, sourceText, err := clipSvc.BuildClipContext(ctx, payload.ClipIDs, opts)
	if err != nil {
		return nil, fmt.Errorf("clip context building failed: %w", err)
	}
	sourceFingerprint := clipSvc.ComputeFingerprint(payload.ClipIDs, pack, opts, NewFingerprintContext(opts.Model, opts.Model))
	if tools != nil && tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine")
	}
	writeResult, err := pu.engine.WriteScript(ctx, WriteScriptRequest{
		Plan: &scriptpkg.ScriptGenerationPlan{
			Title:       plan.Title,
			Topic:       plan.Title,
			Language:    opts.Language,
			Tone:        opts.Tone,
			Model:       opts.Model,
			Mode:        gemmamemory.ModeClipToScript,
			SourceText:  sourceText,
			TargetWords: opts.TargetWords,
			UseMemory:   !payload.ForceRefresh,
			SaveToDB:    payload.SaveToDB,
			Prompt:      sourceFingerprint,
		},
		Topic:       plan.Title,
		Title:       plan.Title,
		Language:    opts.Language,
		Tone:        opts.Tone,
		Model:       opts.Model,
		Mode:        gemmamemory.ModeClipToScript,
		SourceText:  sourceText,
		MinWords:    opts.TargetWords,
		Prompt:      sourceFingerprint,
		UseMemory:   !payload.ForceRefresh,
		SaveToDB:    payload.SaveToDB,
		SaveTimeout: 60,
		ClipPack:    pack,
	})
	if err != nil {
		return nil, fmt.Errorf("clip-script generation failed: %w", err)
	}
	clipScenes := BuildScenesWithMarkers(writeResult.Script, pack)
	if pu.log != nil {
		pu.log.Info("clip-script generated",
			zap.Int("scenes", len(clipScenes)),
			zap.Int("words", writeResult.WordCount),
			zap.Int("clip_anchored", SceneCountWithKind(clipScenes, "clip")),
			zap.Int("narration_anchored", SceneCountWithKind(clipScenes, "narration")))
	}
	return &ClipSourcePathResult{
		WriteResult:       writeResult,
		ClipScenes:        clipScenes,
		SourceFingerprint: sourceFingerprint,
	}, nil
}

// handleClipPathAutoSearch — moved inline 1:1 from the previous handler.
func (pu *PipelineUseCase) handleClipPathAutoSearch(ctx context.Context, payload *scriptpkg.GenerationSpec, tools *appjobs.JobTools) (*ClipSourcePathResult, error) {
	if pu.log != nil {
		pu.log.Info("auto-search path: searching clips via media curator",
			zap.String("topic", payload.Topic),
			zap.Int("num_clips", payload.NumClips))
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(10, "Searching for clips and generating script...")
	}

	curateReq := CurateRequest{
		Query:             payload.Topic,
		Title:             payload.Title,
		Language:          payload.Language,
		Tone:              payload.Tone,
		Model:             payload.Model,
		MaxClips:          payload.NumClips,
		TargetWords:       payload.TargetWords,
		StyleInstructions: payload.Guidelines,
		ForceRefresh:      payload.ForceRefresh,
	}
	curateResult, err := pu.mediaCurator.Curate(ctx, curateReq)
	if err != nil {
		return nil, fmt.Errorf("auto-search generation failed: %w", err)
	}
	if payload.Title == "" && curateResult.Title != "" {
		payload.Title = curateResult.Title
	}
	writeResult := &WriteScriptResult{
		Script:      curateResult.Script,
		WordCount:   curateResult.WordCount,
		EstDuration: (curateResult.WordCount * 60) / 150,
		Model:       payload.Model,
		Prompt:      curateResult.SourceFingerprint,
		CacheStatus: curateResult.CacheStatus,
		WasCached:   curateResult.CacheStatus == "exact_hit",
	}
	if pu.log != nil {
		pu.log.Info("auto-search script generated",
			zap.Int("scenes", len(curateResult.ClipScenes)),
			zap.Int("words", writeResult.WordCount),
			zap.String("cache_status", writeResult.CacheStatus))
	}
	return &ClipSourcePathResult{
		WriteResult:       writeResult,
		ClipScenes:        curateResult.ClipScenes,
		SourceFingerprint: curateResult.SourceFingerprint,
		SearchResults:     curateResult.SearchResults,
		NarrativePlan:     curateResult.NarrativePlan,
		CurateTimings:     curateResult.Timings,
	}, nil
}

// handleClipPathTextOnly — moved inline 1:1 from the previous handler.
func (pu *PipelineUseCase) handleClipPathTextOnly(ctx context.Context, payload *scriptpkg.GenerationSpec, tools *appjobs.JobTools) (*ClipSourcePathResult, error) {
	if payload.NumClips > 0 && len(payload.ClipIDs) == 0 {
		if pu.log != nil {
			pu.log.Warn("media curator not available, falling back to text-only generation",
				zap.Int("num_clips", payload.NumClips))
		}
	}
	if pu.log != nil {
		pu.log.Info("text-only path: generating script from topic",
			zap.String("topic", payload.Topic))
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(20, "Generating script text...")
	}

	minFloor := 100
	if pu.cfg != nil && pu.cfg.minWordFloor > 0 {
		minFloor = pu.cfg.minWordFloor
	}
	topic := strings.TrimSpace(payload.Topic)
	if topic == "" {
		topic = strings.TrimSpace(payload.SourceText)
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = topic
	}
	if payload.TargetWords <= 0 {
		payload.TargetWords = minFloor
		if payload.TargetWords <= 0 {
			payload.TargetWords = 100
		}
	}
	if payload.Model == "" && pu.cfg != nil {
		payload.Model = pu.cfg.ollamaModel
	}
	plan := BuildTextOnlyScriptPlan(
		topic, payload.SourceText, payload.Guidelines, title,
		payload.Language, payload.Tone, payload.Model,
		payload.ForceRefresh, payload.SaveToDB, payload.TargetWords,
		defaultsString(payload.PromptVersion, DefaultTextPromptVersion),
		defaultsString(payload.EditorPromptVersion, DefaultTextEditorPromptVersion),
		defaultsString(payload.QAPromptVersion, DefaultTextQAPromptVersion),
	)
	writeResult, err := pu.engine.WriteScript(ctx, WriteScriptRequest{
		Plan:        plan,
		SaveTimeout: 60,
	})
	if err != nil {
		return nil, fmt.Errorf("text script generation failed: %w", err)
	}
	if pu.log != nil {
		pu.log.Info("text script generated",
			zap.Int64("script_id", writeResult.ScriptID),
			zap.Int("words", writeResult.WordCount))
	}
	return &ClipSourcePathResult{
		WriteResult:       writeResult,
		SourceFingerprint: writeResult.Prompt,
	}, nil
}

// buildFinalResult — moved inline 1:1 from the previous handler.
func (pu *PipelineUseCase) buildFinalResult(
	payload *scriptpkg.GenerationSpec,
	pathResult *ClipSourcePathResult,
	entitiesJSON string,
	insights ScriptInsights,
	videoMetadata []VideoMetadata,
	docLink, docID string,
	scenes []SceneImage,
	voiceovers []SceneVoiceover,
	totalDurMs int64,
) map[string]any {
	if pathResult == nil || pathResult.WriteResult == nil {
		return map[string]any{"ok": false}
	}
	out := map[string]any{
		"ok":           true,
		"script":       pathResult.WriteResult.Script,
		"word_count":   pathResult.WriteResult.WordCount,
		"title":        payload.Title,
		"language":     payload.Language,
		"cache_status": pathResult.WriteResult.CacheStatus,
	}
	if payload.ExtractEntities {
		out["entities_json"] = entitiesJSON
		out["important_words"] = insights.ImportantWords
		out["important_phrases"] = insights.ImportantPhrases
		out["special_names"] = insights.SpecialNames
		out["artlist_phrases"] = insights.ArtlistPhrases
		out["artlist_clip_suggestions"] = insights.ArtlistClipSuggestions
		out["recommended_drive_folder"] = insights.RecommendedDriveFolder
		out["phrase_clip_suggestions"] = insights.PhraseClipSuggestions
		out["intro_clips"] = insights.IntroClips
		out["entity_images"] = insights.EntityImages
	}
	if payload.GenerateSceneImages {
		out["scenes"] = scenes
		if b, err := json.Marshal(scenes); err == nil {
			out["scenes_json"] = string(b)
		}
	}
	if len(voiceovers) > 0 {
		out["voiceovers"] = voiceovers
		for _, vo := range voiceovers {
			if vo.Status == "completed" {
				if vo.Link != "" {
					out["voiceover_path"] = vo.Link
					out["audio_path"] = vo.Link
				} else if vo.LocalPath != "" {
					out["voiceover_path"] = vo.LocalPath
					out["audio_path"] = vo.LocalPath
				}
				break
			}
		}
	}
	if payload.GenerateMetadata {
		out["metadata"] = videoMetadata
	}
	if docLink != "" {
		out["doc_url"] = docLink
		out["doc_id"] = docID
	}
	if len(pathResult.ClipScenes) > 0 {
		out["clip_scenes"] = pathResult.ClipScenes
		out["clip_count"] = len(pathResult.ClipScenes)
	}
	if len(pathResult.SearchResults) > 0 {
		out["search_results"] = pathResult.SearchResults
	}
	if pathResult.NarrativePlan != nil {
		out["narrative_plan"] = pathResult.NarrativePlan
	}
	if pathResult.CurateTimings.TotalMs > 0 {
		out["curate_timings"] = map[string]any{
			"search_ms":        pathResult.CurateTimings.SearchMs,
			"build_context_ms": pathResult.CurateTimings.BuildCtxMs,
			"write_script_ms":  pathResult.CurateTimings.WriteScriptMs,
			"total_ms":         pathResult.CurateTimings.TotalMs,
		}
	}
	out["timings"] = map[string]any{"total_ms": totalDurMs}
	return out
}

// defaultsString is a tiny inline default-coalesce that mirrors
// pkg/defaults.String without taking the import in this file's path.
func defaultsString(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

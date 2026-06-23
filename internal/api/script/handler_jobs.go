// Package script (api/script) — handler_jobs.go carries every job-system
// handler used by the script-flow transport plus the inline catalog.Svc
// and curation.Svc types whose concrete wires are still TBD.
//
// PR3 (June 2026): this file consolidates the four prior job_handler*
// files plus the catalog/ and curation/ sub-directory service
// implementations. They share the same ScriptFlowHandler receiver
// (or, in the case of catalog/curation, share the future ScriptFlowDeps
// CurationJobService/CatalogJobService port — interfaces in helpers.go).
//
// Job types touched:
//
//   script.generate_batch           (HandleBatchScriptGenerateJob)
//   script.generate_from_clips      (HandleClipScriptGenerateJob; 3 paths)
//   script.generate_from_catalog    (catalog.Service.HandleCatalogScriptGenerateJob)
//   script.curate                   (curation.Service.HandleCurateJob)
//
// The catalog.Service and curation.Service types are kept as inline
// non-method types because the WireRegistry never wires them today
// (ScriptFlowDeps.Curation/CatalogJobService are nil — PR4.E, June 2026).
// This means they are "future-ready": when the next PR wires them in,
// no API churn is required; only the registry call needs to construct
// and pass them through ScriptFlowDeps.{Curation,Catalog}JobService.
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Job registration ─────────────────────────────────────────────────────────

// RegisterJobHandlers registers the handlers for script jobs.
func (h *ScriptFlowHandler) RegisterJobHandlers(jobsSvc *job.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(job.TypeBatchScriptGenerate, h.HandleBatchScriptGenerateJob)
		h.log.Info("registered script.generate_batch job handler")
		jobsSvc.RegisterHandler(job.TypeClipScriptGenerate, h.HandleClipScriptGenerateJob)
		h.log.Info("registered script.generate_from_clips job handler")
		if h.catalogJobService != nil {
			jobsSvc.RegisterHandler(job.TypeCatalogScriptGenerate, h.catalogJobService.HandleCatalogScriptGenerateJob)
			h.log.Info("registered script.generate_from_catalog job handler")
		}
		if h.curationJobService != nil {
			jobsSvc.RegisterHandler("script.curate", h.curationJobService.HandleCurateJob)
			h.log.Info("registered script.curate job handler")
		}
	}
}

// HandleBatchScriptGenerateJob processes the background job for script.generate_batch.
func (h *ScriptFlowHandler) HandleBatchScriptGenerateJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_batch job", zap.String("job_id", job.ID))
	var req scripts.GenerateBatchRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	// Make sure Async is false inside execution to prevent re-enqueueing
	req.Async = false

	var progressFunc func(int, string)
	if tools != nil && tools.Progress != nil {
		progressFunc = func(pct int, msg string) {
			tools.Progress(pct, msg)
		}
	}

	if h.batchService == nil {
		return nil, fmt.Errorf("batch service not initialized")
	}
	resp, err := h.batchService.Execute(ctx, &req, progressFunc)
	if err != nil {
		return nil, err
	}
	// Convert typed response to map for the job system.
	return resp.ToMap(), nil
}

// ── Path result type & stage logger ──────────────────────────────────────────

// clipSourcePathResult is the result produced by a single script generation path.
// The same struct is used regardless of which path was taken (clip, auto-search, text-only).
type clipSourcePathResult struct {
	WriteResult       *scripts.WriteScriptResult
	ClipScenes        []scripts.ClipScene
	SourceFingerprint string
	SearchResults     []scripts.SearchResultInfo
	NarrativePlan     *scripts.NarrativePlan
	CurateTimings     scripts.CurateTimings
}

// stageLog wraps a pipeline phase with structured start/complete logs so
// operators can see exactly where the job is (or stuck) by watching the
// pipelinegen log stream. Cheap to add: single-duration computation, one zap
// call per edge. Returns a function that records the end of the stage with
// caller-supplied extra fields (status, counts, ms sub-timing).
func stageLog(log *zap.Logger, jobID, stage string) func(extra ...zap.Field) {
	t := time.Now()
	log.Info("pipeline_stage_started",
		zap.String("job_id", jobID),
		zap.String("stage", stage))
	return func(extra ...zap.Field) {
		fields := append([]zap.Field{
			zap.String("job_id", jobID),
			zap.String("stage", stage),
			zap.Int64("duration_ms", time.Since(t).Milliseconds()),
		}, extra...)
		log.Info("pipeline_stage_completed", fields...)
	}
}

// wrapPostGeneration is the 3-arg delegate that scripts.NewPipeline expects
// for its post-generation hook position (5° positional arg). It builds a
// canonical-script-only default pathResult and delegates to the full
// wrapPostGenerationWithPath so the Pipeline type stays decoupled from
// ScriptFlowHandler-only fields (ClipScenes / SearchResults / NarrativePlan
// / CurateTimings). If Pipeline ever surfaces a real pathResult as
// closure, swap this method's body — callers won't notice.
func (h *ScriptFlowHandler) wrapPostGeneration(
	ctx context.Context,
	spec *script.GenerationSpec,
	scriptBody string,
) (entitiesJSON string, insights any, videoMetadata []scripts.VideoMetadata) {
	return h.wrapPostGenerationWithPath(ctx, spec, scriptBody, &clipSourcePathResult{
		WriteResult: &scripts.WriteScriptResult{Script: scriptBody},
	})
}

// wrapPostGenerationWithPath is called by HandleClipScriptGenerateJob with the
// real pathResult so handlePostGeneration has access to all path result fields.
func (h *ScriptFlowHandler) wrapPostGenerationWithPath(
	ctx context.Context,
	spec *script.GenerationSpec,
	script string,
	pathResult *clipSourcePathResult,
) (entitiesJSON string, insights any, videoMetadata []scripts.VideoMetadata) {
	if pathResult == nil {
		pathResult = &clipSourcePathResult{
			WriteResult: &scripts.WriteScriptResult{Script: script},
		}
	}
	ents, ins, meta := h.handlePostGeneration(ctx, spec, pathResult)

	docMeta := make([]scripts.VideoMetadata, len(meta))
	for i, m := range meta {
		docMeta[i] = scripts.VideoMetadata{
			Language:    m.Language,
			Title:       m.Title,
			Description: m.Description,
			Tags:        m.Tags,
		}
	}
	return ents, ins, docMeta
}

// ── Unified clip-source job handler ─────────────────────────────────────────

// HandleClipScriptGenerateJob processes the unified script generation job.
// Supports three paths:
//   - Explicit clip IDs (clip_ids provided -> handleClipPathExplicit)
//   - Auto-search (num_clips > 0, no clip_ids -> handleClipPathAutoSearch)
//   - Text-only (fallback -> handleClipPathTextOnly)
//
// Phase graph (post script_generation):
//
//	Phase 2 (parallel fan-out): entity_metadata ‖ scene_images
//	  Both stages only require pathResult (script). Wall time reduces from
//	  entityT + scenesT  →  max(entityT, scenesT).
//	Phase 3 (sequential):       scene_voiceovers (depends on scenes)
//	Phase 4 (sequential):       google_doc (depends on all)
//
// Each phase emits pipeline_stage_started / _completed zap logs with
// duration_ms so operators can pinpoint exactly where a job is stalled.
func (h *ScriptFlowHandler) HandleClipScriptGenerateJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	h.log.Info("handling unified script generation job", zap.String("job_id", job.ID))

	maxCap := cap(h.scriptGenSem)
	h.log.Info("waiting for script generation slot", zap.String("job_id", job.ID), zap.Int("max_concurrent", maxCap))
	select {
	case h.scriptGenSem <- struct{}{}:
		h.log.Info("acquired script generation slot", zap.String("job_id", job.ID))
		defer func() {
			<-h.scriptGenSem
			h.log.Info("released script generation slot", zap.String("job_id", job.ID))
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	startAll := time.Now()

	genPayload, err := script.DecodeGeneratePayload(job.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode job payload: %w", err)
	}
	spec := &genPayload.Spec

	// Construct application-layer services using dependencies available
	// on the handler. This avoids touching app wiring.
	scenesSvc := scripts.NewScenesService(
		h.clipServices.ImgSvc,
		h.clipServices.VoSvc,
		h.log,
		h.cfg,
		h.resolveDriveFolderID,
		h.groupsResolver,
		0, // use VELOX_SCENE_PARALLELISM env var
	)
	docsSvc := scripts.NewDocumentsService(h.docClient, h.log, h.driveFolderID)
	pipeline := scripts.NewPipeline(
		h.log,
		job.ID,
		scenesSvc,
		docsSvc,
		h.wrapPostGeneration,
		h.resolveDriveFolderID,
	)

	// Phase 0 (best-effort Playwright prewarm): fire sidecar POST /prewarm-pages
	// in parallel with Phase 1's LLM call. By the time generateSceneImages (Phase 2)
	// runs, the Playwright tab pool is warm, saving ~30s first-scene cold-start.
	// Gated on payload flags: pure text-only jobs skip prewarm (no ImgSvc path),
	// saving 1-5s Python startup + asyncio gather cost for nothing.
	// Triggered AFTER scriptGenSemaphore acquire so we never warm a tab that would
	// age out (CONTEXT_MAX_AGE=30m) while waiting in the queue. Best-effort by design.
	if h.clipServices.ImgSvc != nil &&
		(spec.GenerateSceneImages || len(spec.ClipIDs) > 0 || spec.NumClips > 0) {
		go func() {
			prewarmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			h.clipServices.ImgSvc.TriggerPrewarm(prewarmCtx, job.ID, 4)
		}()
	}

	h.log.Info("pipeline_dispatch_decided",
		zap.String("job_id", job.ID),
		zap.Int("clip_ids", len(spec.ClipIDs)),
		zap.Int("num_clips", spec.NumClips),
		zap.Bool("extract_entities", spec.ExtractEntities),
		zap.Bool("generate_scene_images", spec.GenerateSceneImages),
		zap.Bool("generate_voiceover", spec.GenerateVoiceover),
		zap.Int("sentences_per_image", spec.SentencesPerImage),
		zap.Int("images_per_scene", spec.ImagesPerScene),
		zap.String("language", spec.Language),
		zap.String("style", spec.Style))

	// Phase 1: dispatch to path
	var pathResult *clipSourcePathResult
	stagePath := stageLog(h.log, job.ID, "script_generation")
	pathStart := time.Now()

	// Path dispatch: surface misconfiguration explicitly instead of silently falling back
	// to text-only when the user requested a clip-aware flow but the required
	// builder is unavailable.
	switch {
	case len(spec.ClipIDs) > 0:
		if h.clipSourceBuilder == nil {
			return nil, fmt.Errorf("clip pipeline unavailable: %d clip_ids provided but clipSourceBuilder is not initialized in this deployment; check app wiring (SetClipSourceBuilder)", len(spec.ClipIDs))
		}
		pathResult, err = h.handleClipPathExplicit(ctx, spec, tools)
	case spec.NumClips > 0:
		if h.mediaCurator == nil {
			return nil, fmt.Errorf("auto-search pipeline unavailable: num_clips=%d requested but mediaCurator is not initialized in this deployment; check app wiring (SetMediaCurator)", spec.NumClips)
		}
		pathResult, err = h.handleClipPathAutoSearch(ctx, spec, tools)
	default:
		pathResult, err = h.handleClipPathTextOnly(ctx, spec, tools)
	}
	if err != nil {
		stagePath(zap.String("status", "failed"), zap.String("error", err.Error()))
		return nil, err
	}
	stagePath(
		zap.String("status", "ok"),
		zap.Int("script_chars", len(pathResult.WriteResult.Script)),
		zap.Int("word_count", pathResult.WriteResult.WordCount),
		zap.String("cache_status", pathResult.WriteResult.CacheStatus),
		zap.Int64("path_ms", time.Since(pathStart).Milliseconds()))

	// Phases 2-4: post-generation pipeline (entity_metadata, scene_images,
	// scene_voiceovers, google_doc). Delegated to the application-layer Pipeline.
	// Pass the real pathResult so post-generation has access to all path fields.
	pipelineResult, pipeErr := pipeline.Run(ctx, spec, pathResult.WriteResult.Script, tools)
	if pipeErr != nil {
		return nil, pipeErr
	}

	// Total wall time from job start (includes Phase 0–4).
	totalDurMs := time.Since(startAll).Milliseconds()

	h.log.Info("pipeline_completed",
		zap.String("job_id", job.ID),
		zap.Int64("total_ms", totalDurMs),
		zap.Int("scenes", len(pipelineResult.Scenes)),
		zap.Int("voiceovers", len(pipelineResult.Voiceovers)),
		zap.Bool("has_doc", pipelineResult.DocLink != ""))

	if tools.Progress != nil {
		tools.Progress(100, "Generation completed")
	}

	// Convert pipeline result types to handler-local types where needed.
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

	return h.buildFinalResult(spec, pathResult,
		pipelineResult.EntitiesJSON,
		scriptInsights,
		scriptMeta,
		pipelineResult.DocLink,
		pipelineResult.DocID,
		pipelineResult.Scenes,
		pipelineResult.Voiceovers,
		totalDurMs), nil
}

// ── Three clip-source paths ──────────────────────────────────────────────────

// sceneCountWithKind counts how many ClipScenes have a matching Kind value.
// Used for diagnostic logging in the clip-aware paths so we can report
// "clip-anchored" vs "narration-anchored" scene counts separately.
func sceneCountWithKind(scenes []scripts.ClipScene, kind string) int {
	n := 0
	for _, s := range scenes {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// handleClipPathExplicit is Path 1: generate script from explicit clip IDs.
func (h *ScriptFlowHandler) handleClipPathExplicit(ctx context.Context, payload *script.GenerationSpec, tools *appjobs.JobTools) (*clipSourcePathResult, error) {
	h.log.Info("clip-aware path: generating script from explicit clip IDs",
		zap.Int("clip_ids", len(payload.ClipIDs)))

	if tools.Progress != nil {
		tools.Progress(10, "Loading clips and building evidence cards")
	}

	clipSvc := h.clipSourceBuilder
	// Surface all client-supplied filters and style guidance to the builder.
	// Previously these fields were silently dropped here, so callers passing
	// min_quality_score / min_transcript_words / guidelines had no effect on
	// the explicit-clip path (auto-search and text-only already honored them).
	opts := &scripts.ClipGenerationOptions{
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

	sourceFingerprint := clipSvc.ComputeFingerprint(payload.ClipIDs, pack, opts, scripts.NewFingerprintContext(opts.Model, opts.Model))

	if tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine")
	}

	writeResult, err := h.engine.WriteScript(ctx, scripts.WriteScriptRequest{
		Plan: &script.ScriptGenerationPlan{
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

	// BuildScenesWithMarkers prefers LLM-emitted `[Clip: id]` markers when
	// present (precise alignment), with a round-robin fallback for any
	// clips the LLM omitted. Guarantees 1:1 coverage with pack.Clips so
	// downstream scene_images / voiceovers / result mapping can rely on
	// scene N ↔ clip N correspondence.
	clipScenes := scripts.BuildScenesWithMarkers(writeResult.Script, pack)
	h.log.Info("clip-script generated",
		zap.Int("scenes", len(clipScenes)),
		zap.Int("words", writeResult.WordCount),
		zap.Int("clip_anchored", sceneCountWithKind(clipScenes, "clip")),
		zap.Int("narration_anchored", sceneCountWithKind(clipScenes, "narration")))

	return &clipSourcePathResult{
		WriteResult:       writeResult,
		ClipScenes:        clipScenes,
		SourceFingerprint: sourceFingerprint,
	}, nil
}

// handleClipPathAutoSearch is Path 2: search clips via MediaCurator and generate script.
func (h *ScriptFlowHandler) handleClipPathAutoSearch(ctx context.Context, payload *script.GenerationSpec, tools *appjobs.JobTools) (*clipSourcePathResult, error) {
	h.log.Info("auto-search path: searching clips via media curator",
		zap.String("topic", payload.Topic),
		zap.Int("num_clips", payload.NumClips))

	if tools.Progress != nil {
		tools.Progress(10, "Searching for clips and generating script...")
	}

	curateReq := scripts.CurateRequest{
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

	curateResult, err := h.mediaCurator.Curate(ctx, curateReq)
	if err != nil {
		return nil, fmt.Errorf("auto-search generation failed: %w", err)
	}

	if payload.Title == "" && curateResult.Title != "" {
		payload.Title = curateResult.Title
	}

	writeResult := &scripts.WriteScriptResult{
		Script:      curateResult.Script,
		WordCount:   curateResult.WordCount,
		EstDuration: (curateResult.WordCount * 60) / 150,
		Model:       payload.Model,
		Prompt:      curateResult.SourceFingerprint,
		CacheStatus: curateResult.CacheStatus,
		WasCached:   curateResult.CacheStatus == "exact_hit",
	}

	h.log.Info("auto-search script generated",
		zap.Int("scenes", len(curateResult.ClipScenes)),
		zap.Int("words", writeResult.WordCount),
		zap.String("cache_status", writeResult.CacheStatus))

	return &clipSourcePathResult{
		WriteResult:       writeResult,
		ClipScenes:        curateResult.ClipScenes,
		SourceFingerprint: curateResult.SourceFingerprint,
		SearchResults:     curateResult.SearchResults,
		NarrativePlan:     curateResult.NarrativePlan,
		CurateTimings:     curateResult.Timings,
	}, nil
}

// handleClipPathTextOnly is Path 3: text-only script generation (fallback).
// Also used when the user requested clips but the curator was unavailable.
func (h *ScriptFlowHandler) handleClipPathTextOnly(ctx context.Context, payload *script.GenerationSpec, tools *appjobs.JobTools) (*clipSourcePathResult, error) {
	// Log a warning if user wanted clips but curator was unavailable
	if payload.NumClips > 0 && len(payload.ClipIDs) == 0 {
		h.log.Warn("media curator not available, falling back to text-only generation",
			zap.Int("num_clips", payload.NumClips))
	}

	h.log.Info("text-only path: generating script from topic",
		zap.String("topic", payload.Topic))

	if tools.Progress != nil {
		tools.Progress(20, "Generating script text...")
	}

	scriptCfg := config.ScriptsConfig{}
	if h.cfg != nil {
		scriptCfg = h.cfg.Scripts.WithDefaults()
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
		payload.TargetWords = scriptCfg.MinWordFloor
		if payload.TargetWords <= 0 {
			payload.TargetWords = 100
		}
	}

	if payload.Model == "" && h.cfg != nil {
		payload.Model = h.cfg.External.OllamaModel
	}

	plan := buildTextOnlyScriptPlan(
		topic, payload.SourceText, payload.Guidelines, title,
		payload.Language, payload.Tone, payload.Model,
		payload.ForceRefresh, payload.SaveToDB, payload.TargetWords,
		defaults.String(payload.PromptVersion, scripts.DefaultTextPromptVersion),
		defaults.String(payload.EditorPromptVersion, scripts.DefaultTextEditorPromptVersion),
		defaults.String(payload.QAPromptVersion, scripts.DefaultTextQAPromptVersion),
	)

	writeResult, err := h.engine.WriteScript(ctx, scripts.WriteScriptRequest{
		Plan:        plan,
		SaveTimeout: 60,
	})
	if err != nil {
		return nil, fmt.Errorf("text script generation failed: %w", err)
	}

	h.log.Info("text script generated",
		zap.Int64("script_id", writeResult.ScriptID),
		zap.Int("words", writeResult.WordCount))

	return &clipSourcePathResult{
		WriteResult:       writeResult,
		SourceFingerprint: writeResult.Prompt,
	}, nil
}

// ── Post-generation phases ──────────────────────────────────────────────────

// handlePostGeneration runs entity extraction, insight building, and metadata
// generation in parallel when the corresponding flags are set.
// Returns the raw entities JSON, structured insights, and per-language video metadata.
func (h *ScriptFlowHandler) handlePostGeneration(
	ctx context.Context,
	payload *script.GenerationSpec,
	pathResult *clipSourcePathResult,
) (entitiesJSON string, insights ScriptInsights, videoMetadata []VideoMetadata) {
	if !payload.ExtractEntities && !payload.GenerateMetadata {
		return "", ScriptInsights{}, nil
	}
	if pathResult == nil || pathResult.WriteResult == nil {
		return "", ScriptInsights{}, nil
	}

	group, groupCtx := concurrent.WithContext(ctx)

	if payload.ExtractEntities {
		group.Go("entities-and-insights", func() error {
			var client EntityScriptExtractor
			if h.generator != nil {
				client = h.generator.GetClient()
			}
			ents, err := ExtractScriptEntities(groupCtx, client, pathResult.WriteResult.Script, h.metadataModel)
			if err != nil {
				h.log.Warn("failed to extract entities", zap.Error(err))
			}
			entitiesJSON = ents
			if h.insightBuilder != nil {
				insights = h.insightBuilder.Build(groupCtx, payload.Title, pathResult.WriteResult.Script, ents)
			}
			return nil
		})
	}

	if payload.GenerateMetadata {
		group.Go("video-metadata", func() error {
			languages := BuildMetadataLanguages(payload.Language, payload.Languages)
			videoMetadata = GenerateVideoMetadata(groupCtx, h.generator, payload.Title, languages, h.metadataModel)
			return nil
		})
	}

	if waitErr := group.Wait(); waitErr != nil {
		h.log.Warn("post-generation phase returned an error (continuing)", zap.Error(waitErr))
	}

	return entitiesJSON, insights, videoMetadata
}

// buildFinalResult assembles the final result map sent back to the client.
func (h *ScriptFlowHandler) buildFinalResult(
	payload *script.GenerationSpec,
	pathResult *clipSourcePathResult,
	entitiesJSON string,
	insights ScriptInsights,
	videoMetadata []VideoMetadata,
	docLink, docID string,
	scenes []ScriptSceneImage,
	voiceovers []SceneVoiceover,
	totalDurMs int64,
) map[string]any {
	if pathResult == nil || pathResult.WriteResult == nil {
		return map[string]any{"ok": false}
	}

	result := map[string]any{
		"ok":           true,
		"script":       pathResult.WriteResult.Script,
		"word_count":   pathResult.WriteResult.WordCount,
		"title":        payload.Title,
		"language":     payload.Language,
		"cache_status": pathResult.WriteResult.CacheStatus,
	}

	if payload.ExtractEntities {
		result["entities_json"] = entitiesJSON
		result["important_words"] = insights.ImportantWords
		result["important_phrases"] = insights.ImportantPhrases
		result["special_names"] = insights.SpecialNames
		result["artlist_phrases"] = insights.ArtlistPhrases
		result["artlist_clip_suggestions"] = insights.ArtlistClipSuggestions
		result["recommended_drive_folder"] = insights.RecommendedDriveFolder
		result["phrase_clip_suggestions"] = insights.PhraseClipSuggestions
		result["intro_clips"] = insights.IntroClips
		result["entity_images"] = insights.EntityImages
	}
	if payload.GenerateSceneImages {
		result["scenes"] = scenes
		if scenesJSONBytes, err := json.Marshal(scenes); err == nil {
			result["scenes_json"] = string(scenesJSONBytes)
		}
	}
	if len(voiceovers) > 0 {
		result["voiceovers"] = voiceovers
		for _, vo := range voiceovers {
			if vo.Status == "completed" {
				if vo.Link != "" {
					result["voiceover_path"] = vo.Link
					result["audio_path"] = vo.Link
				} else if vo.LocalPath != "" {
					result["voiceover_path"] = vo.LocalPath
					result["audio_path"] = vo.LocalPath
				}
				break
			}
		}
	}
	if payload.GenerateMetadata {
		result["metadata"] = videoMetadata
	}
	if docLink != "" {
		result["doc_url"] = docLink
		result["doc_id"] = docID
	}

	if len(pathResult.ClipScenes) > 0 {
		result["clip_scenes"] = pathResult.ClipScenes
		result["clip_count"] = len(pathResult.ClipScenes)
	}

	// Auto-search specific enrichments
	if len(pathResult.SearchResults) > 0 {
		result["search_results"] = pathResult.SearchResults
	}
	if pathResult.NarrativePlan != nil {
		result["narrative_plan"] = pathResult.NarrativePlan
	}
	if pathResult.CurateTimings.TotalMs > 0 {
		result["curate_timings"] = map[string]any{
			"search_ms":        pathResult.CurateTimings.SearchMs,
			"build_context_ms": pathResult.CurateTimings.BuildCtxMs,
			"write_script_ms":  pathResult.CurateTimings.WriteScriptMs,
			"total_ms":         pathResult.CurateTimings.TotalMs,
		}
	}

	result["timings"] = map[string]any{
		"total_ms": totalDurMs,
	}

	return result
}

// ── Inline catalog job service (future-ready) ───────────────────────────────

// CatalogJobServiceImpl satisfies CurationJobService interface
// (defined in helpers.go) for the catalog-to-script background job.
//
// Ctor signature mirrors the prior `internal/api/script/catalog.Service`
// for zero churn when WireRegistry eventually wires this in (it does
// NOT today — see PR4.E, June 2026).
type CatalogJobServiceImpl struct {
	clipSourceBuilder *scripts.ClipSourceBuilder
	engine            *scripts.Engine
	log               *zap.Logger
}

// NewCatalogJobServiceImpl creates the catalog job service.
func NewCatalogJobServiceImpl(
	clipSourceBuilder *scripts.ClipSourceBuilder,
	engine *scripts.Engine,
	log *zap.Logger,
) *CatalogJobServiceImpl {
	return &CatalogJobServiceImpl{
		clipSourceBuilder: clipSourceBuilder,
		engine:            engine,
		log:               log,
	}
}

// HandleCatalogScriptGenerateJob processes a background script.generate_from_catalog job.
// Compile-time assertion that catalogJobService satisfies the CurationJobService-shaped
// port (helpers.go::CatalogJobService).
var _ CatalogJobService = (*CatalogJobServiceImpl)(nil)

// Cast helper for the catalog slot in ScriptFlowDeps.
func (c *CatalogJobServiceImpl) AsPort() CatalogJobService { return c }

// HandleCatalogScriptGenerateJob processes a background script.generate_from_catalog job.
func (c *CatalogJobServiceImpl) HandleCatalogScriptGenerateJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	c.log.Info("handling script.generate_from_catalog job", zap.String("job_id", job.ID))

	clipSvc := c.clipSourceBuilder
	if clipSvc == nil {
		return nil, fmt.Errorf("clip source builder not initialized")
	}
	if c.engine == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}

	var payload scripts.JobPayloadCatalogScript
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Loading %d clips selected from catalog", len(payload.ClipIDs)))
	}

	opts := &scripts.ClipGenerationOptions{
		Language:         payload.Language,
		Tone:             payload.Tone,
		Title:            payload.Title,
		Model:            payload.Model,
		TargetWords:      payload.TargetWords,
		TranscriptPolicy: payload.TranscriptPolicy,
		OrderingStrategy: payload.OrderingStrategy,
	}
	if payload.MinQualityScore != nil {
		opts.MinQualityScore = *payload.MinQualityScore
	}
	if payload.MinTranscriptWords != nil {
		opts.MinTranscriptWords = *payload.MinTranscriptWords
	}

	if tools.Progress != nil {
		tools.Progress(15, "Hydrating clips and building evidence cards")
	}

	pack, plan, sourceText, err := clipSvc.BuildClipContext(ctx, payload.ClipIDs, opts)
	if err != nil {
		return nil, fmt.Errorf("clip context building failed: %w", err)
	}

	sourceFingerprint := clipSvc.ComputeFingerprint(payload.ClipIDs, pack, opts, scripts.NewFingerprintContext(opts.Model, opts.Model))

	if tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine (MemoryGate, normalization)...")
	}

	writeResult, err := c.engine.WriteScript(ctx, scripts.WriteScriptRequest{
		Plan: &script.ScriptGenerationPlan{
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
		Type:        opts.Type,
		ClipPack:    pack,
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	wordCount := textutil.CountWords(writeResult.Script)

	if tools.Progress != nil {
		tools.Progress(90, "Finalizing...")
	}

	result := map[string]any{
		"ok":           true,
		"script_id":    writeResult.ScriptID,
		"title":        plan.Title,
		"script":       writeResult.Script,
		"word_count":   wordCount,
		"language":     opts.Language,
		"mode":         "catalog_first",
		"cache_status": writeResult.CacheStatus,
		"clip_coverage": map[string]any{
			"requested": len(payload.ClipIDs),
			"accepted":  len(pack.Clips),
			"used":      len(plan.OrderedClips),
			"excluded":  len(pack.ExcludedClips),
		},
		"narrative_arc":      plan.NarrativeArc,
		"warnings":           plan.Warnings,
		"sections_count":     len(plan.OrderedClips),
		"source_fingerprint": sourceFingerprint,
		"narrative_plan":     plan,
	}

	if len(pack.ExcludedClips) > 0 {
		excluded := make([]map[string]any, 0, len(pack.ExcludedClips))
		for _, ec := range pack.ExcludedClips {
			excluded = append(excluded, map[string]any{
				"clip_id": ec.ClipID,
				"reason":  ec.ExcludeReason,
			})
		}
		result["excluded_clips"] = excluded
	}

	if tools.Progress != nil {
		tools.Progress(100, "Catalog-first generation completed")
	}

	return result, nil
}

// ── Inline curation job service (future-ready) ──────────────────────────────

// CurationJobServiceImpl satisfies CurationJobService interface
// (helpers.go::CurationJobService) for the script.curate background job.
type CurationJobServiceImpl struct {
	mediaCurator   *scripts.MediaCurator
	voService      *voiceover.Service
	cfg            *config.Config
	log            *zap.Logger
	resolveFolder  func(ctx context.Context, input, defaultRootID string) (string, error)
	groupsResolver *voiceover.GroupsResolver
	maybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string)
}

// NewCurationJobServiceImpl creates the curation job service.
func NewCurationJobServiceImpl(
	mediaCurator *scripts.MediaCurator,
	voService *voiceover.Service,
	cfg *config.Config,
	log *zap.Logger,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	groupsResolver *voiceover.GroupsResolver,
	maybeCreateDoc func(ctx context.Context, title, content, folderID string, createDoc bool) (string, string),
) *CurationJobServiceImpl {
	return &CurationJobServiceImpl{
		mediaCurator:   mediaCurator,
		voService:      voService,
		cfg:            cfg,
		log:            log,
		resolveFolder:  resolveFolder,
		groupsResolver: groupsResolver,
		maybeCreateDoc: maybeCreateDoc,
	}
}

// Compile-time assertion that CurationJobServiceImpl satisfies CurationJobService.
var _ CurationJobService = (*CurationJobServiceImpl)(nil)

// AsPort exposes the struct as the narrow port for ScriptFlowDeps wiring.
func (c *CurationJobServiceImpl) AsPort() CurationJobService { return c }

// ── Curation helpers (kept private to file) ─────────────────────────────────

type voiceoverSceneItem struct {
	Text       string
	SceneIndex int
}

// buildVoiceoverDestination builds a *voiceover.DestinationRequest from the
// provided parameters.
func buildVoiceoverDestination(
	ctx context.Context,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	log *zap.Logger,
	title, voiceoverFolderID, voiceoverGroup, voRootID string,
	groupsResolver *voiceover.GroupsResolver,
) *voiceover.DestinationRequest {
	subfolderName := textutil.SlugifyWithMax(title, 40)

	if folderID := strings.TrimSpace(voiceoverFolderID); folderID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        folderID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	if groupsResolver != nil && strings.TrimSpace(voiceoverGroup) != "" {
		entry, err := groupsResolver.ResolveByName(ctx, voRootID, voiceoverGroup)
		switch {
		case err == nil && entry.FolderID != "":
			if log != nil {
				log.Info("routed voiceover via DB groups_resolver",
					zap.String("voiceover_group", voiceoverGroup),
					zap.String("folder_id", entry.FolderID),
					zap.String("parent_id", voRootID))
			}
			return &voiceover.DestinationRequest{
				FolderID:        entry.FolderID,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		case err != nil && !errors.Is(err, voiceover.ErrGroupNotFound):
			if log != nil {
				log.Warn("groups_resolver lookup failed unexpectedly, falling back to Drive deep-search",
					zap.String("voiceover_group", voiceoverGroup),
					zap.Error(err))
			}
		}
	}

	targetFolderOrGroup := voiceoverGroup
	if targetFolderOrGroup != "" && resolveFolder != nil {
		resolvedVOFolder, err := resolveFolder(ctx, targetFolderOrGroup, voRootID)
		if err != nil {
			if log != nil {
				log.Warn("failed to resolve custom voiceover folder name/path, using default root", zap.Error(err))
			}
			resolvedVOFolder = voRootID
		}
		if resolvedVOFolder != "" {
			return &voiceover.DestinationRequest{
				FolderID:        resolvedVOFolder,
				SubfolderName:   subfolderName,
				CreateSubfolder: true,
			}
		}
	}

	if voRootID != "" {
		return &voiceover.DestinationRequest{
			FolderID:        voRootID,
			SubfolderName:   subfolderName,
			CreateSubfolder: true,
		}
	}

	grp := voiceoverGroup
	if grp == "" {
		grp = "curation"
	}
	return &voiceover.DestinationRequest{
		Group:           grp,
		SubfolderName:   subfolderName,
		CreateSubfolder: true,
	}
}

// generateSceneVoiceovers generates voiceovers for each scene item.
func generateSceneVoiceovers(
	ctx context.Context,
	voService *voiceover.Service,
	scenes []voiceoverSceneItem,
	language string,
	destReq *voiceover.DestinationRequest,
	log *zap.Logger,
	onProgress func(pct int, msg string),
	basePct, pctRange int,
) int {
	if voService == nil || destReq == nil || len(scenes) == 0 {
		return 0
	}
	voCtx := context.WithoutCancel(ctx)
	successCount := 0
	for i, sc := range scenes {
		sceneText := strings.TrimSpace(sc.Text)
		if sceneText == "" {
			continue
		}
		sceneSlug := textutil.SlugifyWithMax(sceneText, 30)
		filename := sceneSlug

		if onProgress != nil && len(scenes) > 0 {
			onProgress(basePct+(i*pctRange/len(scenes)), "")
		}

		voRes, voErr := voService.GenerateWithDestination(voCtx, sceneText, language, filename, destReq)
		if voErr != nil {
			if log != nil {
				log.Warn("voiceover generation failed for scene",
					zap.Int("scene_index", sc.SceneIndex),
					zap.Error(voErr))
			}
			continue
		}
		if voRes != nil {
			successCount++
		}
	}
	return successCount
}

// buildCurateDocContent builds HTML content for a Google Doc from curate output.
func buildCurateDocContent(title string, clipScenes []scripts.ClipScene) string {
	var b strings.Builder
	b.WriteString("<html><head><style>")
	b.WriteString("body { font-family: Arial, Helvetica, sans-serif; font-size: 11pt; line-height: 1.4; margin: 20px; }")
	b.WriteString("h1 { font-family: Arial, sans-serif; font-size: 16pt; font-weight: bold; }")
	b.WriteString("h2 { font-family: Arial, sans-serif; font-size: 13pt; font-weight: bold; margin-top: 20px; }")
	b.WriteString("p { font-family: Arial, Helvetica, sans-serif; font-size: 11pt; line-height: 1.6; margin: 10px 0; }")
	b.WriteString(".scene-label { font-family: Arial, sans-serif; font-size: 10pt; color: #666; margin-top: 18px; margin-bottom: 2px; }")
	b.WriteString(".scene-meta { font-family: Arial, sans-serif; font-size: 9pt; color: #444; font-style: italic; margin: 2px 0 4px 4px; }")
	b.WriteString(".scene-preview { font-family: Arial, sans-serif; font-size: 9pt; color: #555; background: #f7f7f7; padding: 6px 10px; border-left: 3px solid #ccc; margin: 4px 0 6px 4px; }")
	b.WriteString(".drive-link { font-family: Arial, sans-serif; font-size: 9pt; color: #1a73e8; margin: 4px 0 6px 4px; }")
	b.WriteString("</style></head><body>")
	b.WriteString("<h1>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1>")

	for _, sc := range clipScenes {
		words := countWords(sc.Text)
		duration := approxReadingSeconds(words)

		if sc.ClipID != "" {
			b.WriteString(fmt.Sprintf(
				"<p class=\"scene-label\">🎬 Scene %d — Clip: %s</p>",
				sc.SceneIndex, html.EscapeString(sc.ClipID)))
		} else {
			label := "Intro"
			if sc.SceneIndex > 1 {
				if isLikelyOutro(sc, clipScenes) {
					label = "Outro"
				} else {
					label = "Transition"
				}
			}
			fmt.Fprintf(&b, "<p class=\"scene-label\">📝 Scene %d — %s</p>", sc.SceneIndex, label)
		}

		b.WriteString(fmt.Sprintf(
			"<p class=\"scene-meta\">~%d words · ~%ds read</p>",
			words, duration))

		if preview := firstSentencePreview(sc.Text, 140); preview != "" {
			b.WriteString("<p class=\"scene-preview\">")
			b.WriteString(html.EscapeString(preview))
			b.WriteString("</p>")
		}

		if sc.ClipID != "" && sc.DriveLink != "" {
			b.WriteString(fmt.Sprintf(
				"<p class=\"drive-link\"><a href=\"%s\">Drive link</a></p>",
				html.EscapeString(sc.DriveLink)))
		}

		for _, para := range strings.Split(sc.Text, "\n") {
			para = strings.TrimSpace(para)
			if para != "" {
				b.WriteString("<p>")
				b.WriteString(html.EscapeString(para))
				b.WriteString("</p>")
			}
		}
	}

	b.WriteString("</body></html>")
	return b.String()
}

func countWords(text string) int {
	return len(strings.Fields(text))
}

func approxReadingSeconds(words int) int {
	if words <= 0 {
		return 0
	}
	return max(1, (words*60)/150)
}

func firstSentencePreview(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = textutil.StripNarrationMarkerRe.ReplaceAllString(text, "")
	text = textutil.StripClipMarkerRe.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	cutAt := -1
	for _, sep := range []string{". ", "!\n", "?\n", ".\n"} {
		if i := strings.Index(text, sep); i > 0 {
			if cutAt < 0 || i < cutAt {
				cutAt = i + len(sep)
			}
		}
	}
	preview := text
	if cutAt > 0 {
		preview = text[:cutAt]
	}
	preview = strings.TrimRight(preview, " \t\n")
	preview = strings.TrimSuffix(preview, ".")

	if len(preview) > maxChars {
		truncated := preview[:maxChars]
		if i := strings.LastIndex(truncated, " "); i > maxChars/2 {
			truncated = truncated[:i]
		}
		preview = strings.TrimRight(truncated, " ,;:") + "..."
	} else {
		preview += "."
	}
	return preview
}

func isLikelyOutro(sc scripts.ClipScene, all []scripts.ClipScene) bool {
	if sc.ClipID != "" {
		return false
	}
	if sc.SceneIndex == len(all) {
		return true
	}
	narrationAfter := 0
	for _, c := range all {
		if c.SceneIndex > sc.SceneIndex && c.ClipID == "" {
			narrationAfter++
		}
	}
	return narrationAfter == 0
}

// ── HandleCurateJob (full body) ──────────────────────────────────────────────

// HandleCurateJob processes a background script.curate job.
func (c *CurationJobServiceImpl) HandleCurateJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	c.log.Info("handling script.curate job", zap.String("job_id", job.ID))

	curator := c.mediaCurator
	if curator == nil {
		return nil, fmt.Errorf("media curator not initialized")
	}

	var payload scripts.JobPayloadCurate
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	c.log.Info("curate job params",
		zap.String("query", payload.Query),
		zap.String("language", payload.Language),
		zap.String("tone", payload.Tone),
		zap.Int("max_clips", payload.MaxClips),
		zap.Int("target_words", payload.TargetWords))

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Searching clips for: %s", payload.Query))
	}

	req := scripts.CurateRequest{
		Query:             payload.Query,
		Title:             payload.Title,
		Language:          payload.Language,
		Tone:              payload.Tone,
		Model:             payload.Model,
		MaxClips:          payload.MaxClips,
		SelectableClips:   payload.SelectableClips,
		TargetWords:       payload.TargetWords,
		MaxCharsPerScene:  payload.MaxCharsPerScene,
		MinScore:          payload.MinScore,
		Source:            payload.Source,
		MediaType:         payload.MediaType,
		Type:              payload.Type,
		Style:             payload.Style,
		StyleInstructions: payload.StyleInstructions,
		ForceRefresh:      payload.ForceRefresh,
	}

	if tools.Progress != nil {
		tools.Progress(15, "Semantic search complete, building clip context...")
	}

	result, err := curator.Curate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("curation failed: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(90, "Creating Google Doc...")
	}

	var docLink, docID, docErr string
	docContent := buildCurateDocContent(result.Title, result.ClipScenes)
	if c.maybeCreateDoc != nil {
		if l, id := c.maybeCreateDoc(ctx, result.Title, docContent, "", true); l != "" {
			docLink = l
			docID = id
		}
	}
	if docLink == "" {
		docErr = "google doc creation failed (non-fatal)"
		c.log.Warn("Google Doc creation failed, continuing without it")
	}

	voiceoverResults := make([]map[string]any, 0)
	if payload.GenerateVoiceover && c.voService != nil && len(result.ClipScenes) > 0 {
		if tools.Progress != nil {
			tools.Progress(95, "Generating voiceovers for each scene...")
		}

		voRootID := payload.VoiceoverFolderID
		if voRootID == "" && c.cfg != nil {
			voRootID = c.cfg.Drive.VoiceoverFolder()
		}
		destReq := buildVoiceoverDestination(
			ctx, c.resolveFolder, c.log, result.Title,
			payload.VoiceoverFolderID, payload.VoiceoverGroup,
			voRootID, c.groupsResolver,
		)
		if destReq != nil {
			scenes := make([]voiceoverSceneItem, len(result.ClipScenes))
			for i, sc := range result.ClipScenes {
				scenes[i] = voiceoverSceneItem{Text: sc.Text, SceneIndex: sc.SceneIndex}
			}
			generateSceneVoiceovers(ctx, c.voService, scenes, payload.Language, destReq, c.log, tools.Progress, 95, 5)
		}
	}

	if tools.Progress != nil {
		tools.Progress(100, "Curation completed")
	}

	clipScenesJSON := make([]map[string]any, 0, len(result.ClipScenes))
	for _, sc := range result.ClipScenes {
		m := map[string]any{
			"scene_index": sc.SceneIndex,
			"text":        sc.Text,
		}
		if sc.ClipID != "" {
			m["clip_id"] = sc.ClipID
		}
		if sc.DriveLink != "" {
			m["drive_link"] = sc.DriveLink
		}
		clipScenesJSON = append(clipScenesJSON, m)
	}

	searchResultsJSON := make([]map[string]any, 0, len(result.SearchResults))
	for _, sr := range result.SearchResults {
		m := map[string]any{
			"clip_id": sr.ClipID,
			"name":    sr.Name,
			"score":   sr.Score,
		}
		if sr.Source != "" {
			m["source"] = sr.Source
		}
		if sr.DriveLink != "" {
			m["drive_link"] = sr.DriveLink
		}
		searchResultsJSON = append(searchResultsJSON, m)
	}

	response := map[string]any{
		"ok":                 true,
		"title":              result.Title,
		"script":             result.Script,
		"word_count":         result.WordCount,
		"language":           payload.Language,
		"tone":               payload.Tone,
		"cache_status":       result.CacheStatus,
		"accepted_clip_ids":  result.AcceptedClipIDs,
		"clip_scenes":        clipScenesJSON,
		"search_results":     searchResultsJSON,
		"narrative_plan":     result.NarrativePlan,
		"source_text":        result.SourceText,
		"source_fingerprint": result.SourceFingerprint,
		"voiceover_results":  voiceoverResults,
		"timings": map[string]any{
			"search_ms":        result.Timings.SearchMs,
			"build_context_ms": result.Timings.BuildCtxMs,
			"write_script_ms":  result.Timings.WriteScriptMs,
			"total_ms":         result.Timings.TotalMs,
		},
	}

	if docLink != "" {
		response["doc_link"] = docLink
		response["doc_id"] = docID
	}
	if docErr != "" {
		response["doc_error"] = docErr
	}

	return response, nil
}

// (PR3 fixup: dropped association / realtime imports — they were not used
//  in this file, only the comment claimed so. Go imports are file-scoped;
//  associate.realtime references live in flow.go (SearchScriptAssets,
//  filterSearchAssets) and helpers.go.)

package script

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/documents"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/scenes"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptflow/curation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
)

// clipSourcePathResult is the result produced by a single script generation path.
// The same struct is used regardless of which path was taken (clip, auto-search, text-only).
type clipSourcePathResult struct {
	WriteResult       *scripts.WriteScriptResult
	ClipScenes        []scripts.ClipScene
	SourceFingerprint string
	SearchResults     []curation.SearchResultInfo
	NarrativePlan     *scripts.NarrativePlan
	CurateTimings     curation.CurateTimings
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

// scriptGenSemaphore limits the number of concurrent script generation processes to 2
// to prevent overloading CPU/GPU models and Playwright browser instances.
var scriptGenSemaphore = make(chan struct{}, 2)

// wrapPostGeneration adapts handlePostGeneration to the Pipeline's callback
// signature (returns any instead of ScriptInsights, []documents.VideoMetadata
// instead of []VideoMetadata). The real pathResult is passed through so
// handlePostGeneration has access to all path result fields.
func (h *ScriptFlowHandler) wrapPostGeneration(
	ctx context.Context,
	spec *script.GenerationSpec,
	script string,
) (entitiesJSON string, insights any, videoMetadata []documents.VideoMetadata) {
	return h.wrapPostGenerationWithPath(ctx, spec, script, nil)
}

// wrapPostGenerationWithPath is called by HandleClipScriptGenerateJob with the
// real pathResult so handlePostGeneration has access to all path result fields.
func (h *ScriptFlowHandler) wrapPostGenerationWithPath(
	ctx context.Context,
	spec *script.GenerationSpec,
	script string,
	pathResult *clipSourcePathResult,
) (entitiesJSON string, insights any, videoMetadata []documents.VideoMetadata) {
	if pathResult == nil {
		pathResult = &clipSourcePathResult{
			WriteResult: &scripts.WriteScriptResult{Script: script},
		}
	}
	ents, ins, meta := h.handlePostGeneration(ctx, spec, pathResult)

	docMeta := make([]documents.VideoMetadata, len(meta))
	for i, m := range meta {
		docMeta[i] = documents.VideoMetadata{
			Language:    m.Language,
			Title:       m.Title,
			Description: m.Description,
			Tags:        m.Tags,
		}
	}
	return ents, ins, docMeta
}

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
func (h *ScriptFlowHandler) HandleClipScriptGenerateJob(ctx context.Context, job *jobservice.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling unified script generation job", zap.String("job_id", job.ID))

	h.log.Info("waiting for script generation slot (max 2 concurrent)", zap.String("job_id", job.ID))
	select {
	case scriptGenSemaphore <- struct{}{}:
		h.log.Info("acquired script generation slot", zap.String("job_id", job.ID))
		defer func() {
			<-scriptGenSemaphore
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
	scenesSvc := scenes.NewService(
		h.clipServices.ImgSvc,
		h.clipServices.VoSvc,
		h.log,
		h.cfg,
		h.resolveDriveFolderID,
		h.groupsResolver,
		0, // use VELOX_SCENE_PARALLELISM env var
	)
	docsSvc := documents.NewService(h.docClient, h.log, h.driveFolderID)
	pipeline := jobs.NewPipeline(
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
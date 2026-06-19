package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/content/mediacurator"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// jobPayloadUnified is the runtime payload for the unified script generation job.
type jobPayloadUnified struct {
	// Text generation
	Topic      string `json:"topic"`
	SourceText string `json:"source_text"`
	Guidelines string `json:"guidelines"`

	// Clip-aware
	ClipIDs  []string `json:"clip_ids"`
	NumClips int      `json:"num_clips"`

	// Identity
	Title      string `json:"title"`
	OutputName string `json:"output_name"`
	Language   string `json:"language"`
	Tone       string `json:"tone"`
	Style      string `json:"style"`
	Model      string `json:"model"`

	// Sizing
	TargetWords int `json:"target_words"`
	Duration    int `json:"duration"`
	MinWords    int `json:"min_words"`

	// Feature flags
	ExtractEntities     bool   `json:"extract_entities"`
	GenerateSceneImages bool   `json:"generate_scene_images"`
	ArtlistSearch       bool   `json:"artlist_search"`
	StockSearch         bool   `json:"stock_search"`
	GenerateMetadata    bool   `json:"generate_metadata"`
	GenerateVoiceover   bool   `json:"generate_voiceover"`
	VoiceoverGroup      string `json:"voiceover_group,omitempty"`
	VoiceoverFolderID   string `json:"voiceover_folder_id,omitempty"`

	// Multilingual
	Languages []string `json:"languages"`

	// Clip pipeline
	TranscriptPolicy string `json:"transcript_policy"`
	OrderingStrategy string `json:"ordering_strategy"`
	SaveToDB         bool   `json:"save_to_db"`
	GenerateTimeline bool   `json:"generate_timeline"`
	ForceRefresh     bool   `json:"force_refresh"`

	// Quality
	MinQualityScore    float64 `json:"min_quality_score"`
	MinTranscriptWords int     `json:"min_transcript_words"`

	// Drive
	DriveFolderID string `json:"drive_folder_id"`

	// Prompt versioning
	PromptVersion       string `json:"prompt_version"`
	EditorPromptVersion string `json:"editor_prompt_version"`
	QAPromptVersion     string `json:"qa_prompt_version"`

	// Scene images config
	SentencesPerImage int `json:"sentences_per_image"`
	ImagesPerScene    int `json:"images_per_scene"`
}

// clipSourcePathResult is the result produced by a single script generation path.
// The same struct is used regardless of which path was taken (clip, auto-search, text-only).
type clipSourcePathResult struct {
	WriteResult       *scripts.WriteScriptResult
	ClipScenes        []scripts.ClipScene
	SourceFingerprint string
	SearchResults     []mediacurator.SearchResultInfo
	NarrativePlan     *scripts.NarrativePlan
	CurateTimings     mediacurator.CurateTimings
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

	var payload jobPayloadUnified
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	// Phase 0 (best-effort Playwright prewarm): fire sidecar POST /prewarm-pages
	// in parallel with Phase 1's LLM call. By the time generateSceneImages (Phase 2)
	// runs, the Playwright tab pool is warm, saving ~30s first-scene cold-start.
	// Gated on payload flags: pure text-only jobs skip prewarm (no ImgSvc path),
	// saving 1-5s Python startup + asyncio gather cost for nothing.
	// Triggered AFTER scriptGenSemaphore acquire so we never warm a tab that would
	// age out (CONTEXT_MAX_AGE=30m) while waiting in the queue. Best-effort by design.
	if h.clipServices.ImgSvc != nil &&
		(payload.GenerateSceneImages || len(payload.ClipIDs) > 0 || payload.NumClips > 0) {
		go func() {
			prewarmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			h.clipServices.ImgSvc.TriggerPrewarm(prewarmCtx, job.ID, 4)
		}()
	}

	h.log.Info("pipeline_dispatch_decided",
		zap.String("job_id", job.ID),
		zap.Int("clip_ids", len(payload.ClipIDs)),
		zap.Int("num_clips", payload.NumClips),
		zap.Bool("extract_entities", payload.ExtractEntities),
		zap.Bool("generate_scene_images", payload.GenerateSceneImages),
		zap.Bool("generate_voiceover", payload.GenerateVoiceover),
		zap.Int("sentences_per_image", payload.SentencesPerImage),
		zap.Int("images_per_scene", payload.ImagesPerScene),
		zap.String("language", payload.Language),
		zap.String("style", payload.Style))

	// Phase 1: dispatch to path
	var pathResult *clipSourcePathResult
	var err error
	stagePath := stageLog(h.log, job.ID, "script_generation")
	pathStart := time.Now()

	// Path dispatch: surface misconfiguration explicitly instead of silently falling back
	// to text-only when the user requested a clip-aware flow but the required
	// builder is unavailable.
	switch {
	case len(payload.ClipIDs) > 0:
		if h.clipSourceBuilder == nil {
			return nil, fmt.Errorf("clip pipeline unavailable: %d clip_ids provided but clipSourceBuilder is not initialized in this deployment; check app wiring (SetClipSourceBuilder)", len(payload.ClipIDs))
		}
		pathResult, err = h.handleClipPathExplicit(ctx, &payload, tools)
	case payload.NumClips > 0:
		if h.mediaCurator == nil {
			return nil, fmt.Errorf("auto-search pipeline unavailable: num_clips=%d requested but mediaCurator is not initialized in this deployment; check app wiring (SetMediaCurator)", payload.NumClips)
		}
		pathResult, err = h.handleClipPathAutoSearch(ctx, &payload, tools)
	default:
		pathResult, err = h.handleClipPathTextOnly(ctx, &payload, tools)
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

	// Phase 2 (parallel): entity_metadata ‖ scene_images
	var (
		phase2Mu        sync.Mutex
		phase2Entities  string
		phase2Insights  ScriptInsights
		phase2VideoMeta []VideoMetadata
		phase2Scenes    []ScriptSceneImage
	)
	phase2Run := payload.ExtractEntities || payload.GenerateMetadata || payload.GenerateSceneImages
	if phase2Run {
		if tools.Progress != nil {
			tools.Progress(70, "Phase 2: entities + scene images (parallel)...")
		}
		phase2Start := time.Now()
		h.log.Info("phase2_fanout_started",
			zap.String("job_id", job.ID),
			zap.Bool("entity_metadata_enabled", payload.ExtractEntities || payload.GenerateMetadata),
			zap.Bool("scene_images_enabled", payload.GenerateSceneImages))
		group, groupCtx := concurrent.WithContext(ctx)
		if payload.ExtractEntities || payload.GenerateMetadata {
			group.Go("entity_metadata", func() error {
				stagePost := stageLog(h.log, job.ID, "entity_metadata")
				postStart := time.Now()
				ents, ins, meta := h.handlePostGeneration(groupCtx, &payload, pathResult)
				phase2Mu.Lock()
				phase2Entities = ents
				phase2Insights = ins
				phase2VideoMeta = meta
				phase2Mu.Unlock()
				stagePost(
					zap.Int("entities_chars", len(ents)),
					zap.Int("metadata_count", len(meta)),
					zap.Int64("post_ms", time.Since(postStart).Milliseconds()))
				return nil
			})
		}
		if payload.GenerateSceneImages {
			group.Go("scene_images", func() error {
				stageScenes := stageLog(h.log, job.ID, "scene_images")
				scenesStart := time.Now()
				scns := h.generateSceneImages(groupCtx, &payload, pathResult.WriteResult.Script, tools)
				okScenes := 0
				for _, s := range scns {
					if len(s.Images) > 0 {
						okScenes++
					}
				}
				phase2Mu.Lock()
				phase2Scenes = scns
				phase2Mu.Unlock()
				stageScenes(
					zap.Int("total_scenes", len(scns)),
					zap.Int("ok_scenes", okScenes),
					zap.Int64("scenes_ms", time.Since(scenesStart).Milliseconds()))
				return nil
			})
		}
		if waitErr := group.Wait(); waitErr != nil {
			h.log.Warn("phase2_fanout_partial_errors",
				zap.String("job_id", job.ID), zap.Error(waitErr))
		}
		h.log.Info("phase2_fanout_completed",
			zap.String("job_id", job.ID),
			zap.Int64("phase2_ms", time.Since(phase2Start).Milliseconds()))
	}
	entitiesJSON := phase2Entities
	insights := phase2Insights
	videoMetadata := phase2VideoMeta
	scenes := phase2Scenes

	// Phase 3 (sequential after Phase 2): scene voiceovers
	var voiceovers []SceneVoiceover
	if payload.GenerateVoiceover && h.clipServices.VoSvc != nil {
		if tools.Progress != nil {
			tools.Progress(80, "Generating scene-by-scene voiceovers...")
		}
		stageVoiceover := stageLog(h.log, job.ID, "scene_voiceovers")
		voStart := time.Now()
		voiceovers = h.generateSceneVoiceovers(ctx, &payload, scenes)
		okVoices := 0
		for _, v := range voiceovers {
			if v.Status == "completed" {
				okVoices++
			}
		}
		stageVoiceover(
			zap.Int("voiceover_total", len(voiceovers)),
			zap.Int("voiceover_ok", okVoices),
			zap.Int64("voiceover_ms", time.Since(voStart).Milliseconds()))
	}

	// Phase 4: Google Doc
	if tools.Progress != nil {
		tools.Progress(85, "Creating Google Doc...")
	}
	stageDoc := stageLog(h.log, job.ID, "google_doc")
	docStart := time.Now()
	docLink, docID := h.handleCreateDoc(ctx, &payload, pathResult, entitiesJSON, insights, videoMetadata, scenes)
	if docLink != "" {
		stageDoc(zap.String("status", "ok"), zap.String("doc_id", docID), zap.Int64("doc_ms", time.Since(docStart).Milliseconds()))
	} else {
		stageDoc(zap.String("status", "skipped_or_failed"), zap.Int64("doc_ms", time.Since(docStart).Milliseconds()))
	}

	totalDurMs := time.Since(startAll).Milliseconds()

	h.log.Info("pipeline_completed",
		zap.String("job_id", job.ID),
		zap.Int64("total_ms", totalDurMs),
		zap.Int("scenes", len(scenes)),
		zap.Int("voiceovers", len(voiceovers)),
		zap.Bool("has_doc", docLink != ""))

	if tools.Progress != nil {
		tools.Progress(100, "Generation completed")
	}

	return h.buildFinalResult(&payload, pathResult, entitiesJSON, insights, videoMetadata, docLink, docID, scenes, voiceovers, totalDurMs), nil
}

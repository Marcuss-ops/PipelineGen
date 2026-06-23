// Package script (api/script) — handler_jobs.go carries the job-system
// handler receiver methods for ScriptFlowHandler plus back-compat type
// aliases for CatalogJobServiceImpl and CurationJobServiceImpl.
//
// PR2 (June 2026): standalone helper functions (stageLog, buildVoiceoverDestination,
// generateSceneVoiceovers, buildCurateDocContent) and the CatalogJobServiceImpl /
// CurationJobServiceImpl implementations have been extracted to
// internal/application/scripts/. This file keeps only the ScriptFlowHandler
// receiver methods and thin back-compat wrappers.
package script

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Job registration ─────────────────────────────────────────────────────────

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

func (h *ScriptFlowHandler) HandleBatchScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_batch job", zap.String("job_id", j.ID))
	var req scripts.GenerateBatchRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

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
	return resp.ToMap(), nil
}

// ── stageLog ─────────────────────────────────────────────────────────────────

func stageLog(log *zap.Logger, jobID, stage string) func(extra ...zap.Field) {
	return scripts.StageLog(log, jobID, stage)
}

// ── clipSourcePathResult ────────────────────────────────────────────────────

type clipSourcePathResult = scripts.ClipSourcePathResult

// ── wrapPostGeneration ────────────────────────────────────────────────────────

func (h *ScriptFlowHandler) wrapPostGeneration(
	ctx context.Context,
	spec *scriptpkg.GenerationSpec,
	scriptBody string,
) (entitiesJSON string, insights any, videoMetadata []scripts.VideoMetadata) {
	return h.wrapPostGenerationWithPath(ctx, spec, scriptBody, &scripts.ClipSourcePathResult{
		WriteResult: &scripts.WriteScriptResult{Script: scriptBody},
	})
}

func (h *ScriptFlowHandler) wrapPostGenerationWithPath(
	ctx context.Context,
	spec *scriptpkg.GenerationSpec,
	script string,
	pathResult *scripts.ClipSourcePathResult,
) (entitiesJSON string, insights any, videoMetadata []scripts.VideoMetadata) {
	if pathResult == nil {
		pathResult = &scripts.ClipSourcePathResult{
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

// ── HandleClipScriptGenerateJob ──────────────────────────────────────────────

func (h *ScriptFlowHandler) HandleClipScriptGenerateJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	h.log.Info("handling unified script generation job", zap.String("job_id", j.ID))

	maxCap := cap(h.scriptGenSem)
	h.log.Info("waiting for script generation slot", zap.String("job_id", j.ID), zap.Int("max_concurrent", maxCap))
	select {
	case h.scriptGenSem <- struct{}{}:
		h.log.Info("acquired script generation slot", zap.String("job_id", j.ID))
		defer func() {
			<-h.scriptGenSem
			h.log.Info("released script generation slot", zap.String("job_id", j.ID))
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	startAll := time.Now()

	genPayload, err := scriptpkg.DecodeGeneratePayload(j.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode job payload: %w", err)
	}
	spec := &genPayload.Spec

	scenesSvc := scripts.NewScenesService(
		h.clipServices.ImgSvc,
		h.clipServices.VoSvc,
		h.log,
		h.cfg,
		h.resolveDriveFolderID,
		h.groupsResolver,
		0,
	)
	docsSvc := scripts.NewDocumentsService(h.docClient, h.log, h.driveFolderID)
	pipeline := scripts.NewPipeline(
		h.log,
		j.ID,
		scenesSvc,
		docsSvc,
		h.wrapPostGeneration,
		h.resolveDriveFolderID,
	)

	if h.clipServices.ImgSvc != nil &&
		(spec.GenerateSceneImages || len(spec.ClipIDs) > 0 || spec.NumClips > 0) {
		go func() {
			prewarmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			h.clipServices.ImgSvc.TriggerPrewarm(prewarmCtx, j.ID, 4)
		}()
	}

	h.log.Info("pipeline_dispatch_decided",
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

	var pathResult *scripts.ClipSourcePathResult
	stagePath := stageLog(h.log, j.ID, "script_generation")
	pathStart := time.Now()

	switch {
	case len(spec.ClipIDs) > 0:
		if h.clipSourceBuilder == nil {
			return nil, fmt.Errorf("clip pipeline unavailable: %d clip_ids provided but clipSourceBuilder is not initialized", len(spec.ClipIDs))
		}
		pathResult, err = h.handleClipPathExplicit(ctx, spec, tools)
	case spec.NumClips > 0:
		if h.mediaCurator == nil {
			return nil, fmt.Errorf("auto-search pipeline unavailable: num_clips=%d requested but mediaCurator is not initialized", spec.NumClips)
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

	pipelineResult, pipeErr := pipeline.Run(ctx, spec, pathResult.WriteResult.Script, tools)
	if pipeErr != nil {
		return nil, pipeErr
	}

	totalDurMs := time.Since(startAll).Milliseconds()

	h.log.Info("pipeline_completed",
		zap.String("job_id", j.ID),
		zap.Int64("total_ms", totalDurMs),
		zap.Int("scenes", len(pipelineResult.Scenes)),
		zap.Int("voiceovers", len(pipelineResult.Voiceovers)),
		zap.Bool("has_doc", pipelineResult.DocLink != ""))

	if tools.Progress != nil {
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

func (h *ScriptFlowHandler) handleClipPathExplicit(ctx context.Context, payload *scriptpkg.GenerationSpec, tools *appjobs.JobTools) (*scripts.ClipSourcePathResult, error) {
	h.log.Info("clip-aware path: generating script from explicit clip IDs",
		zap.Int("clip_ids", len(payload.ClipIDs)))

	if tools.Progress != nil {
		tools.Progress(10, "Loading clips and building evidence cards")
	}

	clipSvc := h.clipSourceBuilder
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

	clipScenes := scripts.BuildScenesWithMarkers(writeResult.Script, pack)
	h.log.Info("clip-script generated",
		zap.Int("scenes", len(clipScenes)),
		zap.Int("words", writeResult.WordCount),
		zap.Int("clip_anchored", scripts.SceneCountWithKind(clipScenes, "clip")),
		zap.Int("narration_anchored", scripts.SceneCountWithKind(clipScenes, "narration")))

	return &scripts.ClipSourcePathResult{
		WriteResult:       writeResult,
		ClipScenes:        clipScenes,
		SourceFingerprint: sourceFingerprint,
	}, nil
}

func (h *ScriptFlowHandler) handleClipPathAutoSearch(ctx context.Context, payload *scriptpkg.GenerationSpec, tools *appjobs.JobTools) (*scripts.ClipSourcePathResult, error) {
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

	return &scripts.ClipSourcePathResult{
		WriteResult:       writeResult,
		ClipScenes:        curateResult.ClipScenes,
		SourceFingerprint: curateResult.SourceFingerprint,
		SearchResults:     curateResult.SearchResults,
		NarrativePlan:     curateResult.NarrativePlan,
		CurateTimings:     curateResult.Timings,
	}, nil
}

func (h *ScriptFlowHandler) handleClipPathTextOnly(ctx context.Context, payload *scriptpkg.GenerationSpec, tools *appjobs.JobTools) (*scripts.ClipSourcePathResult, error) {
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

	return &scripts.ClipSourcePathResult{
		WriteResult:       writeResult,
		SourceFingerprint: writeResult.Prompt,
	}, nil
}

// ── handlePostGeneration ─────────────────────────────────────────────────────

func (h *ScriptFlowHandler) handlePostGeneration(
	ctx context.Context,
	payload *scriptpkg.GenerationSpec,
	pathResult *scripts.ClipSourcePathResult,
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

// ── buildFinalResult ─────────────────────────────────────────────────────────

func (h *ScriptFlowHandler) buildFinalResult(
	payload *scriptpkg.GenerationSpec,
	pathResult *scripts.ClipSourcePathResult,
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

// ── Inline catalog/curation job service thin wrappers ────────────────────────

// CatalogJobServiceImpl delegates to the application-layer implementation.
type CatalogJobServiceImpl = scripts.CatalogJobServiceImpl

// NewCatalogJobServiceImpl creates the catalog job service.
var NewCatalogJobServiceImpl = scripts.NewCatalogJobServiceImpl

// CurationJobServiceImpl delegates to the application-layer implementation.
type CurationJobServiceImpl = scripts.CurationJobServiceImpl

// NewCurationJobServiceImpl creates the curation job service.
var NewCurationJobServiceImpl = scripts.NewCurationJobServiceImpl

// ── CatalogJobService + CurationJobService compile-time assertions ──────────

var _ CatalogJobService = (*CatalogJobServiceImpl)(nil)
var _ CurationJobService = (*CurationJobServiceImpl)(nil)

// ── Helpers re-exported from application layer ──────────────────────────────

// buildVoiceoverDestination delegates to the application layer.
func buildVoiceoverDestination(
	ctx context.Context,
	resolveFolder func(ctx context.Context, input, defaultRootID string) (string, error),
	log *zap.Logger,
	title, voiceoverFolderID, voiceoverGroup, voRootID string,
	groupsResolver *voiceover.GroupsResolver,
) *voiceover.DestinationRequest {
	return scripts.BuildVoiceoverDestination(ctx, resolveFolder, log, title, voiceoverFolderID, voiceoverGroup, voRootID, groupsResolver)
}

type voiceoverSceneItem = scripts.VoiceoverSceneItem

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
	return scripts.GenerateSceneVoiceovers(ctx, voService, scenes, language, destReq, log, onProgress, basePct, pctRange)
}

func buildCurateDocContent(title string, clipScenes []scripts.ClipScene) string {
	return scripts.BuildCurateDocContent(title, clipScenes)
}

// ── Text helpers ─────────────────────────────────────────────────────────────

func countWords(text string) int {
	return len(strings.Fields(text))
}

func approxReadingSeconds(words int) int {
	if words <= 0 {
		return 0
	}
	return int(math.Max(1, float64((words*60)/150)))
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
	return scripts.IsLikelyOutro(sc, all)
}

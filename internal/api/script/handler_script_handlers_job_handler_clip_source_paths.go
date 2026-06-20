package script

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

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

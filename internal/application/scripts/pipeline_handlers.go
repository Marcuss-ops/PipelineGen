package scripts

import (
	"context"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

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
	})
	if err != nil {
		return nil, fmt.Errorf("clip-script generation failed: %w", err)
	}
	// PG-033 phase 2 (June 2026): match the canonical signature.
	// BuildScenesWithMarkers now takes both the script and the source
	// clip pack so scene-kind labels can be anchored to clip IDs.
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

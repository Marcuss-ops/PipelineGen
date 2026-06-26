package scripts

import (
	"context"
	"encoding/json"
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
		MaxChars:    payload.MaxChars,
		Prompt:      sourceFingerprint,
		UseMemory:   !payload.ForceRefresh,
		SaveToDB:    payload.SaveToDB,
		SaveTimeout: 60,
	})
	if err != nil {
		return nil, fmt.Errorf("clip-script generation failed: %w", err)
	}
	clipScenes := parseStructuredOutput(writeResult.Script, payload.ClipIDs, payload.MaxChars)
	packMap, packOK := pack.(map[string]any)
	var driveLinks map[string]string
	if packOK {
		if dl, ok := packMap["clip_drive_links"].(map[string]string); ok {
			driveLinks = dl
		}
	}
	if driveLinks != nil {
		for i := range clipScenes {
			if link, ok := driveLinks[clipScenes[i].ClipID]; ok {
				clipScenes[i].DriveLink = link
			}
		}
	}
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

// parseStructuredOutput parses the LLM response as structured JSON when
// MaxChars > 0. Falls back to BuildScenesWithMarkers for prose output.
func parseStructuredOutput(script string, clipIDs []string, maxChars int) []ClipScene {
	if maxChars <= 0 {
		return BuildScenesWithMarkers(script, nil)
	}
	clean := strings.TrimSpace(script)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)
	var pairs []struct {
		ClipID string `json:"clip_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(clean), &pairs); err != nil || len(pairs) == 0 {
		return nil
	}
	clipScenes := make([]ClipScene, 0, len(pairs))
	for i, p := range pairs {
		clipScenes = append(clipScenes, ClipScene{
			SceneIndex: i,
			ClipID:     p.ClipID,
			Text:       p.Text,
			Kind:       "clip",
		})
	}
	return clipScenes
}

// buildFinalResult — moved inline 1:1 from the previous handler.

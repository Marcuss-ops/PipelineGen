package scriptcore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/ml/ollama"
	ollamatypes "velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/gemmamemory"
	"velox/go-master/pkg/retry"

	"go.uber.org/zap"
)

type Engine struct {
	generator   *ollama.Generator
	memorySvc   *gemmamemory.Service
	scriptsRepo *scripts.ScriptRepository
	log         *zap.Logger
}

func NewEngine(gen *ollama.Generator, memSvc *gemmamemory.Service, repo *scripts.ScriptRepository, log *zap.Logger) *Engine {
	return &Engine{
		generator:   gen,
		memorySvc:   memSvc,
		scriptsRepo: repo,
		log:         log,
	}
}

func (e *Engine) CheckMemoryGate(ctx context.Context, channelID, title, prompt, language, mode string, useMemory, forceRefresh bool) (*MemoryGateContext, error) {
	if e.memorySvc == nil || !useMemory {
		return nil, nil
	}

	gateResult, err := e.memorySvc.CheckGate(ctx, gemmamemory.MemoryGateRequest{
		ChannelID:    channelID,
		Title:        title,
		Prompt:       prompt,
		Language:     language,
		Mode:         mode,
		UseMemory:    useMemory,
		ForceRefresh: forceRefresh,
	})
	if err != nil {
		e.log.Warn("memory gate check failed, proceeding without memory", zap.Error(err))
		return nil, nil
	}

	if gateResult == nil {
		return nil, nil
	}

	memCtx := &MemoryGateContext{
		EnrichedPrompt: gateResult.EnrichedPrompt,
		CacheHit:       gateResult.CacheHit,
		SourceGenID:    gateResult.SourceGenerationID,
	}
	if gateResult.ExactOutput != nil {
		memCtx.ExactOutput = gateResult.ExactOutput
	}
	return memCtx, nil
}

func (e *Engine) ResolvePrompt(basePrompt string, memCtx *MemoryGateContext) string {
	if memCtx == nil {
		return basePrompt
	}

	if memCtx.EnrichedPrompt != "" {
		return memCtx.EnrichedPrompt
	}

	if memCtx.CacheHit && memCtx.ExactOutput != nil {
		if output, ok := memCtx.ExactOutput.(*gemmamemory.GenerationOutput); ok {
			return gemmamemory.BuildFreshVariantPrompt(basePrompt, output)
		}
	}

	return basePrompt
}

func (e *Engine) Generate(ctx context.Context, req GenerateRequest) (*GenerateResult, error) {
	if e.generator == nil {
		return nil, fmt.Errorf("script generator not initialized")
	}

	textReq := ollamatypes.TextGenerationRequest{
		Language:        req.Language,
		Duration:        req.Duration,
		DurationMinutes: req.DurationMinutes,
		MinWords:        req.MinWords,
		Tone:            req.Tone,
		Model:           req.Model,
		Prompt:          req.Prompt,
		SourceText:      req.SourceText,
		Title:           req.Title,
		WebContext:      req.WebContext,
	}

	if req.NumPredict > 0 {
		if textReq.Options == nil {
			textReq.Options = make(map[string]any)
		}
		textReq.Options["num_predict"] = req.NumPredict
	}
	if req.Temperature > 0 {
		if textReq.Options == nil {
			textReq.Options = make(map[string]any)
		}
		textReq.Options["temperature"] = req.Temperature
	}

	// PR4 diagnostic: log the actual prompt sent and the raw response received
	// from the writer LLM. Without these, the missing-markers regression is
	// impossible to diagnose from outside the process. The full prompt is large
	// (10–50KB) so we log it at Debug; a truncated preview is logged at Info.
	e.log.Info("engine.Generate: writer LLM call",
		zap.String("model", textReq.Model),
		zap.String("language", textReq.Language),
		zap.String("tone", textReq.Tone),
		zap.Int("min_words", textReq.MinWords),
		zap.Int("source_text_chars", len(textReq.SourceText)),
		zap.Int("prompt_chars", len(textReq.Prompt)),
		zap.Bool("has_output_contract", strings.Contains(textReq.Prompt, "=== OUTPUT CONTRACT ===")),
		zap.Bool("has_strategic_strategy", strings.Contains(textReq.Prompt, "=== STRUCTURAL STRATEGY ===")),
		zap.String("prompt_preview", textReq.Prompt[:min(300, len(textReq.Prompt))]))
	e.log.Debug("engine.Generate: full writer prompt", zap.String("prompt", textReq.Prompt))

	result, err := e.generator.GenerateScript(ctx, textReq)
	if err != nil {
		e.log.Error("engine.Generate: writer LLM call failed", zap.Error(err))
		return nil, err
	}

	// PR4 diagnostic: log the raw LLM response so we can see whether the
	// LLM followed the OUTPUT CONTRACT (emitted [Clip: ...] markers) or
	// ignored it. Truncated preview at Info; full response at Debug.
	rawLen := len(result.Script)
	e.log.Info("engine.Generate: writer LLM response",
		zap.String("model", result.Model),
		zap.Int("response_chars", rawLen),
		zap.Int("response_words", result.WordCount),
		zap.Bool("has_clip_markers", strings.Contains(result.Script, "[Clip:")),
		zap.Bool("has_narration_markers", strings.Contains(result.Script, "[Narration:")),
		zap.String("response_preview", result.Script[:min(400, rawLen)]))
	e.log.Debug("engine.Generate: full writer response", zap.String("script", result.Script))

	return &GenerateResult{
		Script:      result.Script,
		WordCount:   result.WordCount,
		EstDuration: result.EstDuration,
		Model:       result.Model,
		Prompt:      result.Prompt,
	}, nil
}

func (e *Engine) GenerateAndNormalizeWithRetry(ctx context.Context, req GenerateRequest, guidelines string, maxAttempts int) (*GenerateResult, int, error) {
	var retryCount int
	result, err := retry.DoWithValue(ctx, func() (*GenerateResult, error) {
		retryCount++
		return e.GenerateAndNormalize(ctx, req, guidelines)
	}, retry.Options{
		MaxAttempts:    maxAttempts,
		InitialBackoff: 2 * time.Second,
		BackoffFactor:  1.0,
	})
	return result, retryCount, err
}

func (e *Engine) GenerateAndNormalize(ctx context.Context, req GenerateRequest, guidelines string) (*GenerateResult, error) {
	genStart := time.Now()
	result, err := e.Generate(ctx, req)
	genDuration := time.Since(genStart)
	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(result.Script)
	if content == "" {
		return result, nil
	}
	content = ollamatypes.CleanScript(content)

	if req.MinWords > 0 && e.generator != nil {
		// PR4: prefer the scene-aware normalizer so [Clip: ...] and
		// [Narration: ...] markers survive the expand/compress cycle.
		// NormalizeScriptByScenes falls back to legacy NormalizeLength
		// internally when the script has no parseable markers, so this
		// is safe for unstructured scripts too.
		scenes := ParseScenes(content)
		useSceneAware := len(scenes) > 0 && !(len(scenes) == 1 && scenes[0].Kind == "preamble")
		e.log.Info("engine.GenerateAndNormalize: choosing normalizer",
			zap.Bool("scene_aware", useSceneAware),
			zap.Int("parsed_scenes", len(scenes)),
			zap.Int("content_chars", len(content)))

		normalized, wordCount, action, normErr := NormalizeScriptByScenes(ctx, e.generator, req.Language, req.Tone, req.Model, req.Title, content, guidelines, req.MinWords, req.NumPredict)
		if normErr == nil {
			content = normalized
			result.WordCount = wordCount
			result.Script = content
			result.EstDuration = (wordCount * 60) / ollamatypes.WordsPerMinute
			e.log.Info("script normalized",
				zap.String("action", action),
				zap.Int("target", req.MinWords),
				zap.Int("final", wordCount),
				zap.Bool("used_scene_aware", useSceneAware),
				zap.Bool("markers_preserved", strings.Contains(content, "[Clip:")))
		} else {
			e.log.Warn("length normalization failed, using raw output", zap.Error(normErr))
		}
	}

	_ = genDuration
	return result, nil
}

func (e *Engine) SaveMemory(ctx context.Context, channelID, mode, language, title, prompt, model, outputText string, wordCount int) {
	if e.memorySvc == nil {
		return
	}
	_, err := e.memorySvc.SaveAfterGeneration(ctx, gemmamemory.SaveGenerationInput{
		ChannelID:  channelID,
		Mode:       mode,
		Language:   language,
		Title:      title,
		Prompt:     prompt,
		Model:      model,
		OutputText: outputText,
		WordCount:  wordCount,
	}, outputText)
	if err != nil {
		e.log.Warn("failed to save to memory gate", zap.Error(err), zap.String("title", title))
	}
}

func (e *Engine) SaveScript(ctx context.Context, rec *scripts.ScriptRecord, sections []scripts.ScriptSectionRecord, matches []scripts.ScriptStockMatchRecord) (int64, error) {
	if e.scriptsRepo == nil {
		return 0, fmt.Errorf("script repository not available")
	}
	return e.scriptsRepo.SaveScript(ctx, rec, sections, matches)
}

func (e *Engine) SaveResearchSources(ctx context.Context, scriptID int64, sources []scripts.ScriptResearchSource) error {
	if e.scriptsRepo == nil {
		return nil
	}
	return e.scriptsRepo.SaveResearchSources(ctx, scriptID, sources)
}

func (e *Engine) SaveOutlineSections(ctx context.Context, scriptID int64, sections []scripts.ScriptOutlineSectionRecord) error {
	if e.scriptsRepo == nil {
		return nil
	}
	return e.scriptsRepo.SaveOutlineSections(ctx, scriptID, sections)
}

func (e *Engine) LogGeneration(ctx context.Context, scriptID int64, phase, promptHash, model string, inputWords, outputWords int, durationMs int64, retryCount int, cacheStatus, errorMsg string) {
	if e.scriptsRepo == nil || scriptID == 0 {
		return
	}
	_ = e.scriptsRepo.SaveGenerationLog(ctx, scripts.ScriptGenerationLog{
		ScriptID:    scriptID,
		Phase:       phase,
		PromptHash:  promptHash,
		Model:       model,
		InputWords:  inputWords,
		OutputWords: outputWords,
		DurationMs:  durationMs,
		RetryCount:  retryCount,
		CacheStatus: cacheStatus,
		Error:       errorMsg,
	})
}

func (e *Engine) NextVersion(ctx context.Context, topic, language, mode string) int {
	if e.scriptsRepo == nil {
		return 1
	}
	version, err := e.scriptsRepo.NextVersionForTopic(ctx, topic, language, mode)
	if err != nil {
		e.log.Warn("failed to compute script version, falling back to 1", zap.Error(err))
		return 1
	}
	return version
}

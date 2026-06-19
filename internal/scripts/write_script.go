package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	defaults "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

type WriteScriptRequest struct {
	Plan *ScriptGenerationPlan

	Topic        string
	Title        string
	Language     string
	Tone         string
	Model        string
	Mode         string
	ChannelID    string
	UseMemory    bool
	ForceRefresh bool
	SaveToDB     bool

	Duration    int
	MinWords    int
	NumPredict  int
	Temperature float64

	Prompt     string
	SourceText string
	WebContext string
	Guidelines string

	ScriptID int64

	PromptVersion       string
	EditorPromptVersion string
	QAPromptVersion     string

	SaveTimeout int

	// Type is the structural strategy (compilation, story, interview, documentary).
	// Used to resolve the narrative strategy for narration-scene policy.
	// Defaults to "documentary" when empty.
	Type string

	// ClipPack is the optional pack of accepted clip evidence. When set,
	// the engine runs ValidateScriptWithPack after CleanScript and logs
	// the result before SaveMemory. Used by clip-to-script and
	// catalog-first flows; the regular text generation flow leaves this nil.
	ClipPack *ClipSourcePack

	// MaxCharsPerScene is an optional per-scene character limit. When > 0,
	// the validator flags scenes that exceed it. 0 = no limit.
	MaxCharsPerScene int
}

type WriteScriptResult struct {
	ScriptID      int64
	Script        string
	WordCount     int
	EstDuration   int
	Model         string
	Prompt        string
	CacheStatus   string
	WasCached     bool
	ScriptVersion int
}

func (e *Engine) WriteScript(ctx context.Context, req WriteScriptRequest) (*WriteScriptResult, error) {
	if e == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}
	if e.generator == nil {
		return nil, fmt.Errorf("script generator not initialized")
	}

	if req.Plan != nil {
		req.Topic = defaults.String(req.Topic, req.Plan.Topic)
		req.Title = defaults.String(req.Title, req.Plan.Title)
		req.Language = defaults.String(req.Language, req.Plan.Language)
		req.Tone = defaults.String(req.Tone, req.Plan.Tone)
		req.Model = defaults.String(req.Model, req.Plan.Model)
		req.Mode = defaults.String(req.Mode, req.Plan.Mode)
		req.ChannelID = defaults.String(req.ChannelID, req.Plan.ChannelID)
		if !req.UseMemory && req.Plan.UseMemory {
			req.UseMemory = req.Plan.UseMemory
		}
		if req.Plan.ForceRefresh {
			req.ForceRefresh = req.Plan.ForceRefresh
		}
		if !req.SaveToDB && req.Plan.SaveToDB {
			req.SaveToDB = req.Plan.SaveToDB
		}
		req.Duration = defaults.Int(req.Duration, req.Plan.Duration)
		req.MinWords = defaults.Int(req.MinWords, req.Plan.TargetWords)
		req.NumPredict = defaults.Int(req.NumPredict, req.Plan.NumPredict)
		if req.Temperature <= 0 {
			req.Temperature = req.Plan.Temperature
		}
		req.Prompt = defaults.String(req.Prompt, req.Plan.Prompt)
		req.SourceText = defaults.String(req.SourceText, req.Plan.SourceText)
		req.WebContext = defaults.String(req.WebContext, req.Plan.WebContext)
		req.Guidelines = defaults.String(req.Guidelines, req.Plan.Guidelines)
		req.PromptVersion = defaults.String(req.PromptVersion, req.Plan.PromptVersion)
		req.EditorPromptVersion = defaults.String(req.EditorPromptVersion, req.Plan.EditorPromptVersion)
		req.QAPromptVersion = defaults.String(req.QAPromptVersion, req.Plan.QAPromptVersion)
	}

	saveTimeout := 30 * time.Second
	if req.SaveTimeout > 0 {
		saveTimeout = time.Duration(req.SaveTimeout) * time.Second
	}
	saveCtx, saveCancel := context.WithTimeout(context.Background(), saveTimeout)
	defer saveCancel()

	version := 1
	if req.SaveToDB && e.scriptsRepo != nil {
		if nextVersion, versionErr := e.scriptsRepo.NextVersionForTopic(ctx, req.Topic, req.Language, req.Mode); versionErr != nil {
			e.log.Warn("engine.WriteScript: failed to compute script version, falling back to 1", zap.Error(versionErr))
		} else {
			version = nextVersion
			e.log.Info("engine.WriteScript: computed version", zap.Int("version", version), zap.String("topic", req.Topic))
		}
	}

	scriptID := req.ScriptID
	if req.SaveToDB && scriptID == 0 && e.scriptsRepo != nil {
		rec := &ScriptRecord{
			Topic:    req.Topic,
			Title:    req.Title,
			Duration: req.Duration,
			Language: req.Language,
			Template: req.Tone,
			Mode:     req.Mode,
			Tone:     req.Tone,
			TargetWords: func() int {
				if req.MinWords > 0 {
					return req.MinWords
				}
				return CalculateTargetWords(req.Duration, 0)
			}(),
			Version: version,
			Status:  "generating",
		}
		id, err := e.scriptsRepo.SaveScript(saveCtx, rec, nil, nil)
		if err != nil {
			e.log.Warn("engine.WriteScript: pre-save failed, continuing with scriptID=0", zap.Error(err))
		} else {
			scriptID = id
		}
	}

	channelID := defaults.String(req.ChannelID, "default")
	memCtx, _ := e.CheckMemoryGate(ctx, channelID, req.Title, req.Prompt, req.Language, req.Mode, req.UseMemory, req.ForceRefresh)
	// For clip_to_script mode, the Prompt field contains the sourceFingerprint (used as
	// memory gate cache key). Strip it before passing to the generator to avoid injecting
	// a harmless hex string into the LLM prompt. The actual content comes from SourceText.
	promptForGen := req.Prompt
	if req.Mode == gemmamemory.ModeClipToScript {
		promptForGen = ""
	}
	resolvedPrompt := e.ResolvePrompt(promptForGen, memCtx)
	cacheStatus := "fresh"
	wasCached := false
	if memCtx != nil {
		if memCtx.CacheHit && memCtx.ExactOutput != nil {
			cacheStatus = "exact_hit"
			wasCached = true
		} else if memCtx.CacheHit {
			cacheStatus = "reference_hit"
		} else if memCtx.EnrichedPrompt != "" {
			cacheStatus = "enriched"
		}
	}

	if wasCached {
		if output, ok := memCtx.ExactOutput.(*gemmamemory.GenerationOutput); ok {
			if req.SaveToDB && scriptID > 0 && e.scriptsRepo != nil {
				ollamaBaseURL := ""
				if e.generator != nil && e.generator.GetClient() != nil {
					ollamaBaseURL = e.generator.GetClient().BaseURL()
				}

				metaMap := map[string]any{
					"prompt_version":        req.PromptVersion,
					"editor_prompt_version": req.EditorPromptVersion,
					"qa_prompt_version":     req.QAPromptVersion,
					"target_words":          req.MinWords,
				}
				metadataBytes, _ := json.Marshal(metaMap)

				_ = e.scriptsRepo.UpdateScriptFinalContent(saveCtx, scriptID, output.OutputText, textutil.CountWords(output.OutputText), "completed", string(metadataBytes), req.Model, ollamaBaseURL, version)
			}

			return &WriteScriptResult{
				ScriptID:      scriptID,
				Script:        output.OutputText,
				WordCount:     textutil.CountWords(output.OutputText),
				EstDuration:   0,
				Model:         req.Model,
				Prompt:        req.Prompt,
				CacheStatus:   cacheStatus,
				WasCached:     true,
				ScriptVersion: version,
			}, nil
		}
		wasCached = false
		cacheStatus = "reference_hit"
	}

	genReq := GenerateRequest{
		Language:   req.Language,
		Duration:   req.Duration,
		MinWords:   req.MinWords,
		Tone:       req.Tone,
		Model:      req.Model,
		Title:      req.Title,
		Prompt:     resolvedPrompt,
		SourceText: req.SourceText,
		WebContext: req.WebContext,
		ChannelID:  channelID,
		Mode:       req.Mode,
		UseMemory:  req.UseMemory,
		NumPredict: req.NumPredict,
	}
	if req.Temperature > 0 {
		genReq.Temperature = req.Temperature
	}
	result, err := e.GenerateAndNormalize(ctx, genReq, req.Guidelines)
	if err != nil {
		e.LogGeneration(ctx, scriptID, "generate", "", req.Model, textutil.CountWords(req.Prompt), 0, 0, 0, cacheStatus, err.Error())
		return nil, err
	}

	// PR4 — Marker injection: in the clip-to-script flow, always normalize
	// the script string so [Clip: clip_id] markers are visible in the text.
	// BuildScenesWithMarkers handles three cases:
	//   - LLM emitted all markers → preserves the layout verbatim
	//   - LLM emitted partial markers → fills the gaps with round-robin
	//   - LLM emitted no markers → builds scenes from paragraphs + intro/outro
	// RenderScript then re-emits the text with markers on the FIRST line
	// of every scene, so the DB `script` mirrors `clip_scenes[]` 1:1.
	if req.ClipPack != nil {
		// Capture diagnostic signal: how many [Clip: ...] markers did
		// the LLM actually emit? RenderScript will always produce a
		// fully-marked script, so post-mortems need this delta to tell
		// "weak LLM" from "clean run".
		llmEmitted := strings.Count(result.Script, "[Clip:")
		scenes := BuildScenesWithMarkers(result.Script, req.ClipPack)
		result.Script = RenderScript(scenes)
		e.log.Info("marker normalization applied",
			zap.Int("pack_clips", len(req.ClipPack.Clips)),
			zap.Int("llm_emitted_clip_markers", llmEmitted),
			zap.Int("normalized_clip_markers", strings.Count(result.Script, "[Clip:")))
		result.WordCount = textutil.CountWords(result.Script)
	}

	// PR3 — Quality gate: validate the script against the clip pack and
	// log the result BEFORE SaveMemory. In soft mode (current default)
	// the script is still saved; the validation only makes issues
	// visible. Hard mode (skip SaveMemory on Valid=false) is a future
	// tightening; we keep soft mode so a single bad generation does not
	// wipe the cache.
	if req.ClipPack != nil {
		allowNarration := ResolveStrategy(req.Type).AllowNarrationScenes
		validation := ValidateScriptWithPack(result.Script, req.Plan, req.ClipPack, allowNarration, req.MaxCharsPerScene)
		validation.LogWarnings(e.log)
	}

	e.SaveMemory(saveCtx, channelID, req.Mode, req.Language, req.Title, req.Prompt, result.Model, result.Script, result.WordCount)

	e.LogGeneration(saveCtx, scriptID, "generate_"+req.Mode, "", result.Model, textutil.CountWords(req.Prompt), result.WordCount, 0, 0, cacheStatus, "")

	if req.SaveToDB && scriptID > 0 && e.scriptsRepo != nil {
		ollamaBaseURL := ""
		if e.generator != nil && e.generator.GetClient() != nil {
			ollamaBaseURL = e.generator.GetClient().BaseURL()
		}

		metaMap := map[string]any{
			"prompt_version":        req.PromptVersion,
			"editor_prompt_version": req.EditorPromptVersion,
			"qa_prompt_version":     req.QAPromptVersion,
			"target_words":          req.MinWords,
		}
		metadataBytes, _ := json.Marshal(metaMap)

		if updateErr := e.scriptsRepo.UpdateScriptFinalContent(saveCtx, scriptID, result.Script, result.WordCount, "completed", string(metadataBytes), result.Model, ollamaBaseURL, version); updateErr != nil {
			e.log.Warn("engine.WriteScript: failed to update final content in DB", zap.Error(updateErr))
		} else {
			e.log.Info("engine.WriteScript: script status updated to completed in DB", zap.Int64("script_id", scriptID))
		}
	}

	return &WriteScriptResult{
		ScriptID:      scriptID,
		Script:        result.Script,
		WordCount:     result.WordCount,
		EstDuration:   result.EstDuration,
		Model:         result.Model,
		Prompt:        result.Prompt,
		CacheStatus:   cacheStatus,
		WasCached:     wasCached,
		ScriptVersion: version,
	}, nil
}

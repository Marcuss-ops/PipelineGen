package gencore

import (
	"context"
	"fmt"

	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jsonextract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// generationInputs is the resolved, defaulted view of a generation plan that
// the engine phases share.
type generationInputs struct {
	title          string
	topic          string
	language       string
	tone           string
	model          string
	mode           string
	sourceText     string
	renderedPrompt string
	minWords       int
	useMemory      bool
	saveToDB       bool
	cacheKey       string
	skipMemory     bool
	wordsPerMinute int
}

func (e *Engine) resolveGenerationInputs(plan *scriptpkg.ResolvedGenerationPlan) (*generationInputs, error) {
	sourceText, err := ModelSourceText(plan)
	if err != nil {
		return nil, err
	}
	in := &generationInputs{
		title:          plan.Title,
		topic:          plan.Topic,
		language:       plan.Language,
		tone:           plan.Tone,
		model:          plan.Model,
		mode:           plan.Mode,
		sourceText:     sourceText,
		renderedPrompt: plan.RenderedPrompt,
		minWords:       plan.TargetWords,
		useMemory:      plan.UseMemory,
		saveToDB:       plan.SaveToDB,
		cacheKey:       plan.CacheKey,
		skipMemory:     plan.ForceRefresh,
		wordsPerMinute: e.wordsPerMinute,
	}
	if in.topic == "" {
		in.topic = in.title
	}
	if in.title == "" {
		in.title = in.topic
	}
	// Defaults are injected by the composition root after the YAML/env/
	// default/validate resolution pipeline. The package SSOT is retained
	// only for isolated legacy fixtures that do not configure the engine.
	if in.language == "" {
		in.language = e.defaultLanguage
		if in.language == "" {
			in.language = defaults.DefaultScriptConfig().DefaultLanguage
		}
	}
	if in.tone == "" {
		in.tone = e.defaultTone
		if in.tone == "" {
			in.tone = defaults.DefaultScriptConfig().DefaultTone
		}
	}
	if in.wordsPerMinute <= 0 {
		in.wordsPerMinute = defaults.DefaultScriptConfig().WordsPerMinute
	}
	return in, nil
}

func (e *Engine) logGenerationDispatch(in *generationInputs) {
	if e.log == nil {
		return
	}
	// Log source text metrics without ever logging the raw text.
	// SourceTextLogFields emits only hash, length, token estimate
	// and an optional preview.
	e.log.Info("engine: dispatching script generation",
		zap.String("title", in.title),
		zap.String("topic", in.topic),
		zap.String("language", in.language),
		zap.String("tone", in.tone),
		zap.String("model", in.model),
		zap.String("mode", in.mode),
		zap.Int("min_words", in.minWords),
		zap.Bool("use_memory", in.useMemory),
		zap.Bool("force_refresh", in.skipMemory),
		zap.Bool("save_to_db", in.saveToDB),
		zap.Any("source_text", SourceTextLogFields(in.sourceText, processor.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 80})))
}

// generationFromMemory applies the memory gate. The bool reports a hit; a
// decode failure on a hit is returned as an error, exactly as before.
func (e *Engine) generationFromMemory(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, in *generationInputs) (*EngineResult, bool, error) {
	if !in.useMemory || in.skipMemory || e.memorySvc == nil {
		return nil, false, nil
	}
	memoryReq := memoryGateRequest{
		ChannelID: "default",
		Title:     in.title,
		Language:  in.language,
		Mode:      in.mode,
		UseMemory: in.useMemory,
		// ForceRefresh must reach the adapter so the lower layer
		// can apply the same bypass semantics as the use case.
		ForceRefresh: in.skipMemory,
		// PR 2: feed the canonical cache key alongside the
		// legacy Title/Language/Mode lookup. The gemmamemory
		// stub still returns nil; production wiring uses
		// CacheKey when the real Service lands.
		CacheKey: in.cacheKey,
	}
	result, memErr := e.memorySvc.CheckGate(ctx, memoryReq)
	if memErr != nil || result == nil || result.Output == "" {
		return nil, false, nil
	}
	if e.log != nil {
		e.log.Info("engine: memory gate cache hit",
			zap.String("title", in.title),
			zap.Int("word_count", result.WordCount))
	}
	// Cache-replay path uses ModeFreshPlainText (canonical V1-only).
	// Legacy arrays and pre-V1 shapes are no longer supported;
	// migrate with pip-admin migrate-legacy-cache before upgrading.
	scanner := &jsonextract.Scanner{Mode: jsonextract.ModeFreshPlainText}
	output, decodeErr := scanner.Scan([]byte(result.Output), "cache")
	if decodeErr != nil {
		return nil, false, decodeErr
	}
	// PR-CS-1 / FASE 4 (DoD #7): scrub non-prose artefacts
	// (SEGMENT N / Topic: / Source text: / clip_id /
	// accepted_clip_ids / specscene / schema_version /
	// Markdown fences / `# ` comments) BEFORE the engine
	// stamps provenance. Idempotent — replay-safe on the
	// cache-hit path (sanitized text → re-sanitize is a
	// no-op).
	output.Text = SanitizeScriptOutput(output.Text)
	// CODEX-02: the gate compares the whole output with the AGGREGATE
	// segment budget (and each explicit scene budget), never with the
	// first segment's target. Policy default remains observational; the
	// strict policy fails closed instead of serving an over-budget row.
	if err := e.enforceWordBudget(plan, output.Text); err != nil {
		return nil, false, err
	}
	// PR 3 (June 2026): stamp engine-side provenance
	// fields onto the canonical typed MSOV1 so post-
	// processors (notably PersistenceProcessor) read
	// WordCount / ModelUsed / CacheStatus uniformly on
	// the cache-hit path. Without this stamp, replay
	// rows would persist FinalWordCount=0 + empty
	// ModelUsed.
	output.WordCount = result.WordCount
	output.ModelUsed = result.Model
	output.CacheStatus = "exact_hit"
	return &EngineResult{
		Output:        *output,
		WordCount:     result.WordCount,
		Model:         result.Model,
		CacheStatus:   "exact_hit",
		EstDuration:   (result.WordCount * 60) / in.wordsPerMinute,
		ClipEvidence:  plan.ClipEvidence,
		SearchResults: plan.SearchResults,
	}, true, nil
}

func buildTextGenerationRequest(plan *scriptpkg.ResolvedGenerationPlan, in *generationInputs) ports.TextGenerationRequest {
	builtPrompt := in.renderedPrompt
	// PR-CS-1 / FASE 3: ScriptSegment blocks + canonical footer are
	// emitted BEFORE the legacy ClipGroundingInstructions so they
	// take precedence when both are non-empty (the validator layer
	// enforces mutual exclusion at runtime, so the two branches
	// are effectively exclusive in production).
	if segRules := buildSegmentInstructions(plan); segRules != "" {
		if builtPrompt != "" {
			builtPrompt += "\n\n"
		}
		builtPrompt += segRules
	}
	if clipRules := buildClipGroundingInstructions(plan); clipRules != "" {
		if builtPrompt != "" {
			builtPrompt += "\n\n"
		}
		builtPrompt += clipRules
	}
	builtPrompt += plainTextInstruction
	// Research evidence is global for auditability, but the narrative must be
	// partitioned by candidate. Append this contract after the generic prose
	// contract so it is the last editorial instruction seen by the model.
	if plan.ResearchEvidence != nil {
		if narrativePlan := plan.ResearchEvidence.NarrativePlanInstructions(); narrativePlan != "" {
			builtPrompt += "\n\n" + narrativePlan
		}
	}
	return ports.TextGenerationRequest{
		Language:         in.language,
		Tone:             in.tone,
		Model:            in.model,
		Prompt:           builtPrompt,
		SourceText:       in.sourceText,
		Title:            in.title,
		MinWords:         in.minWords,
		MaxChars:         plan.MaxChars,
		Temperature:      plan.Temperature,
		GroundingPolicy:  plan.GroundingPolicy,
		DisableWebSearch: plan.MediaMode == scriptpkg.MediaModeStockOnly || plan.MediaMode == scriptpkg.MediaModeClipOnly,
		// LLM-PLAIN-TEXT-CONTRACT wave (PR-2, July 2026): flip
		// from OutputModeScriptV1 to the canonical OutputModePlainText
		// default. The engine ships raw narrative prose; the
		// downstream SceneSynthesizer + scene binder + postprocessor
		// pipeline own all structured fields (schema_version /
		// specscene / scene IDs / scene indexes / kind labels).
		OutputMode:     ports.OutputModePlainText,
		WordsPerMinute: in.wordsPerMinute,
	}
}

func (e *Engine) logGenerationSuccess(genResult *ports.GenerationResult) {
	if e.log == nil {
		return
	}
	e.log.Info("engine: script generated",
		zap.Int("word_count", genResult.WordCount),
		zap.String("model", genResult.Model),
		zap.Int("est_duration_s", genResult.EstDuration))
}

func (e *Engine) finalizeGeneratedOutput(plan *scriptpkg.ResolvedGenerationPlan, genResult *ports.GenerationResult) (*EngineResult, error) {
	// P0.8 (June 2026) + PR-5 flip + DL-MODECOMPAT-REMOVAL (August 2026):
	// jsonextract.Scanner in ModeFreshPlainText is the SOLE decoder.
	// V1 JSON fast lane first, then ParsePlainTextFresh (canonical
	// primary path for plain prose per the LLM-PLAIN-TEXT-CONTRACT).
	// Legacy arrays and invalid V1 envelopes produce ErrModelOutputMalformed
	// with no retry — the LLM must honour the current contract.
	// ModeCompatibility was removed August 2026 — legacy cache replay
	// of pre-V1 rows uses the admin migrate-legacy-cache tool instead.
	scanner := &jsonextract.Scanner{Mode: jsonextract.ModeFreshPlainText}
	output, decodeErr := scanner.Scan([]byte(genResult.Script), "fresh")
	if decodeErr != nil {
		return nil, fmt.Errorf("engine: model output decode failed: %w", decodeErr)
	}
	// PR-CS-1 / FASE 4 (DoD #7): scrub non-prose artefacts BEFORE
	// stamping. Idempotent — the cache-write path ran the same
	// scrub on the prior write and the cache-hit replay re-runs
	// it (still a no-op on clean input).
	output.Text = SanitizeScriptOutput(output.Text)
	// CODEX-02: same aggregate + per-scene gate as the cache-hit path.
	// Under the strict policy an over-budget script fails closed here,
	// before it is stamped and persisted.
	if err := e.enforceWordBudget(plan, output.Text); err != nil {
		return nil, err
	}
	// PR 3 (June 2026): stamp engine-side provenance fields onto
	// the canonical typed ModelScriptOutputV1 in-place so the
	// typed walk (ppReg.Run with &engineResult.Output) reads
	// WordCount / ModelUsed / CacheStatus directly from the
	// model. Pre-PR-3 ProcessInput envelope carried these as
	// separate fields; PR 3 collapses them onto MSOV1 itself.
	output.WordCount = genResult.WordCount
	output.ModelUsed = genResult.Model
	output.CacheStatus = "generated"
	return &EngineResult{
		Output:        *output,
		WordCount:     genResult.WordCount,
		Model:         genResult.Model,
		CacheStatus:   "generated",
		EstDuration:   genResult.EstDuration,
		ClipEvidence:  plan.ClipEvidence,
		SearchResults: plan.SearchResults,
	}, nil
}

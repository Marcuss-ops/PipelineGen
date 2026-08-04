package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jsonextract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// ── Generate: the canonical engine entry point ────────────────────────

// Generate executes the full generation pipeline for a resolved plan.
// It owns: memory gate check, ollama invocation, and payload decoding.
// It is the ONLY engine entry point for new code.
//
// PR 5 (June 2026): DB persistence is NO LONGER an engine ownership.
// The engine never writes to SQLite; the single writer is
// PersistenceProcessor (registered in the plan's Postprocessors
// list). The engine no longer takes a ScriptRepository constructor
// arg.
//
// Callers: GenerateOneUseCase (canonical — the sole permitted caller).
// Do NOT call Generate from HTTP handlers, source resolvers, or
// postprocessors.
func (e *Engine) Generate(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan) (*EngineResult, error) {
	if e == nil || e.ollamaGen == nil {
		return nil, fmt.Errorf("engine: ollama generator not configured")
	}
	if plan == nil {
		return nil, fmt.Errorf("engine: plan is nil")
	}

	// AZIONE 4 (July 2026): ollamaGen is typed (scriptOllamaGenerator);
	// the nil check above covers the only runtime failure mode.
	// No type assertion needed — the compiler enforces the contract.

	// Extract parameters from the resolved plan.
	title := plan.Title
	topic := plan.Topic
	language := plan.Language
	tone := plan.Tone
	model := plan.Model
	mode := plan.Mode
	sourceText := plan.SourceText
	// PR 2: engine reads RenderedPrompt (editorial instructions).
	// The legacy plan.Prompt field was removed — it conflated
	// fingerprint with model input. RenderedPrompt never contains
	// a fingerprint hash.
	renderedPrompt := plan.RenderedPrompt
	minWords := plan.TargetWords
	useMemory := plan.UseMemory
	saveToDB := plan.SaveToDB
	// PR 2: plan.CacheKey is the canonical memory-gate cache key.
	// It is NEVER sent to the model.
	cacheKey := plan.CacheKey

	// ForceRefresh skips the memory-cache lookup.
	skipMemory := plan.ForceRefresh

	if topic == "" {
		topic = title
	}
	if title == "" {
		title = topic
	}
	// cfg is the canonical script-generation SSOT (language, tone, WPM).
	// All defaults flow from a single call so the intent is visible at a
	// glance and the struct is stack-allocated once per generation.
	cfg := defaults.DefaultScriptConfig()

	if language == "" {
		language = cfg.DefaultLanguage
	}
	if tone == "" {
		tone = cfg.DefaultTone
	}

	if e.log != nil {
		// Log source text metrics without ever logging the raw text.
		// SourceTextLogFields emits only hash, length, token estimate
		// and an optional preview.
		e.log.Info("engine: dispatching script generation",
			zap.String("title", title),
			zap.String("topic", topic),
			zap.String("language", language),
			zap.String("tone", tone),
			zap.String("model", model),
			zap.String("mode", mode),
			zap.Int("min_words", minWords),
			zap.Bool("use_memory", useMemory),
			zap.Bool("force_refresh", skipMemory),
			zap.Bool("save_to_db", saveToDB),
			zap.Any("source_text", SourceTextLogFields(sourceText, adapters.NormalizationConfig{LogSourceTextPreview: true, SourceTextPreviewChars: 80})))
	}

	// Memory gate: check if we have a cached result.
	// ForceRefresh bypasses the cache read even when UseMemory is true.
	// AZIONE 4 (July 2026): memorySvc is typed (memoryGateChecker);
	// no type assertion needed — the compiler enforces the contract.
	if useMemory && !skipMemory && e.memorySvc != nil {
		memoryReq := memoryGateRequest{
			ChannelID: "default",
			Title:     title,
			Language:  language,
			Mode:      mode,
			UseMemory: useMemory,
			// ForceRefresh must reach the adapter so the lower layer
			// can apply the same bypass semantics as the use case.
			ForceRefresh: skipMemory,
			// PR 2: feed the canonical cache key alongside the
			// legacy Title/Language/Mode lookup. The gemmamemory
			// stub still returns nil; production wiring uses
			// CacheKey when the real Service lands.
			CacheKey: cacheKey,
		}
		if result, memErr := e.memorySvc.CheckGate(ctx, memoryReq); memErr == nil && result != nil && result.Output != "" {
			if e.log != nil {
				e.log.Info("engine: memory gate cache hit",
					zap.String("title", title),
					zap.Int("word_count", result.WordCount))
			}
			// P0.8 (June 2026) + post-rename (July 2026): jsonextract.Scanner
			// in ModeCompatibility on the cache-replay path. The fresh
			// generation path (further below) uses ModeFreshPlainText
			// (deprecated same-value alias: ModeStrict). All ModeCompatibility
			// fallbacks are declared and measured via Prometheus counters.
			scanner := &jsonextract.Scanner{Mode: jsonextract.ModeCompatibility}
			output, decodeErr := scanner.Scan([]byte(result.Output), "cache")
			if decodeErr != nil {
				return nil, decodeErr
			}
			// PR-CS-1 / FASE 4 (DoD #7): scrub non-prose artefacts
			// (SEGMENT N / Topic: / Source text: / clip_id /
			// accepted_clip_ids / specscene / schema_version /
			// Markdown fences / `# ` comments) BEFORE the engine
			// stamps provenance. Idempotent — replay-safe on the
			// cache-hit path (sanitized text → re-sanitize is a
			// no-op).
			output.Text = SanitizeScriptOutput(output.Text)
			// PR-CS-1 / FASE 5 (DoD #6): word-budget gate ±25%
			// (observational only — log, never block persistence).
			budget := CheckWordBudget(output.Text, effectiveTargetForBudgetWords(plan))
			if e.log != nil {
				logFields := []zap.Field{
					zap.Int("target_words", budget.TargetWords),
					zap.Int("actual_words", budget.ActualWords),
					zap.Float64("deviation_percent", budget.DeviationPercent),
				}
				if budget.Pass {
					e.log.Info("engine: word-budget gate PASS", logFields...)
				} else {
					e.log.Warn("engine: word-budget gate FAIL", logFields...)
				}
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
				Output:       *output,
				WordCount:    result.WordCount,
				Model:        result.Model,
				CacheStatus:  "exact_hit",
				EstDuration:  (result.WordCount * 60) / cfg.WordsPerMinute,
				ClipEvidence: plan.ClipEvidence,
			}, nil
		}
	}

	// Build ollama request from the resolved plan.
	// PR-CS-1 FASE 14 — emit branch telemetry at the canonical
	// orchestrator site. godlike/06 SSOT: builders must be pure;
	// the orchestrator owns side-effects. This placement also
	// closes the BRANCH-B telemetry gap (SegmentTopics-only plans
	// without clip evidence) — see commit message for details.
	if len(plan.Segments) > 0 {
		RecordScriptGenerationBranch("a", plan.Language)
	} else if len(plan.SegmentTopics) > 0 {
		RecordScriptGenerationBranch("b", plan.Language)
	}
	builtPrompt := renderedPrompt
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

	ollamaReq := ollamatypes.TextGenerationRequest{
		Language:         language,
		Tone:             tone,
		Model:            model,
		Prompt:           builtPrompt,
		SourceText:       sourceText,
		Title:            title,
		MinWords:         minWords,
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
		OutputMode: ollamatypes.OutputModePlainText,
	}

	genResult, err := e.generateSegments(ctx, plan, ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("engine: ollama generation failed: %w", err)
	}

	if e.log != nil {
		e.log.Info("engine: script generated",
			zap.Int("word_count", genResult.WordCount),
			zap.String("model", genResult.Model),
			zap.Int("est_duration_s", genResult.EstDuration))
	}

	// P0.8 (June 2026) + post-rename (July 2026): jsonextract.Scanner
	// in ModeFreshPlainText (canonical; deprecated same-value alias ModeStrict)
	// — V1 JSON fast lane first, then ParsePlainTextFresh (canonical
	// primary path for plain prose per the LLM-PLAIN-TEXT-CONTRACT
	// wave). Legacy arrays and invalid V1 envelopes still produce
	// ErrModelOutputMalformed so the retry path below can downgrade
	// to ModeCompatibility for cache-replay of pre-V1 rows.
	// The cache-replay path at the top of this function uses
	// ModeCompatibility directly (declared fallback with Prometheus
	// metrics).
	scanner := &jsonextract.Scanner{Mode: jsonextract.ModeFreshPlainText}
	output, decodeErr := scanner.Scan([]byte(genResult.Script), "fresh")
	if decodeErr != nil {
		if e.log != nil {
			e.log.Warn("engine: ModeFreshPlainText decode failed, retrying with ModeCompatibility",
				zap.Error(decodeErr))
		}
		scanner.Mode = jsonextract.ModeCompatibility
		output, decodeErr = scanner.Scan([]byte(genResult.Script), "fresh-fallback")
		if decodeErr != nil {
			return nil, fmt.Errorf("engine: model output decode failed (fresh + compatibility): %w", decodeErr)
		}
		if e.log != nil {
			e.log.Info("engine: ModeCompatibility fallback succeeded",
				zap.Int("word_count", genResult.WordCount))
		}
	}

	// PR-CS-1 / FASE 4 (DoD #7): scrub non-prose artefacts BEFORE
	// stamping. Idempotent — the cache-write path ran the same
	// scrub on the prior write and the cache-hit replay re-runs
	// it (still a no-op on clean input).
	output.Text = SanitizeScriptOutput(output.Text)
	// PR-CS-1 / FASE 5 (DoD #6): word-budget gate ±25%
	// (observational only — log, never block persistence).
	budget := CheckWordBudget(output.Text, effectiveTargetForBudgetWords(plan))
	if e.log != nil {
		logFields := []zap.Field{
			zap.Int("target_words", budget.TargetWords),
			zap.Int("actual_words", budget.ActualWords),
			zap.Float64("deviation_percent", budget.DeviationPercent),
		}
		if budget.Pass {
			e.log.Info("engine: word-budget gate PASS", logFields...)
		} else {
			e.log.Warn("engine: word-budget gate FAIL", logFields...)
		}
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

	// PR 5 (June 2026): persistence removed from the engine.
	_ = saveToDB

	return &EngineResult{
		Output:       *output,
		WordCount:    genResult.WordCount,
		Model:        genResult.Model,
		CacheStatus:  "generated",
		EstDuration:  genResult.EstDuration,
		ClipEvidence: plan.ClipEvidence,
	}, nil
}

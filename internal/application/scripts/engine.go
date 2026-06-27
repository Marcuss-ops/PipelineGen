// Package scripts — engine.go provides the canonical script generation
// engine backed by ollama.Generator, gemmamemory.Service, and
// ScriptRepository.
//
// AGENT-3 (June 2026): the previous Engine stub accepted all deps as
// interface{} and returned hardcoded placeholder text. The real
// implementation stores typed fields and calls ollama.Generator
// .GenerateScript.
//
// PG-029 (June 2026): Engine struct consolidated here from the
// now-deleted types.go.
//
// PR 6 (June 2026): canonical Generate(ctx, plan) method — the ONLY
// engine entry point. Accepts a ResolvedGenerationPlan and returns
// a typed EngineResult. The deprecated WriteScript(path) was removed
// in PR 13 (June 2026) after media_curator.go migrated to
// GenerateOneUseCase.
//
// The Engine owns:
//   - ollama script generation (delegates to *ollama.Generator)
//   - memory gate check via gemmamemory (UseMemory path)
//   - script persistence via ScriptRepository (SaveToDB path)
//
// PR 1 (June 2026): payload decoding is owned by the engine via
// internal/application/scripts/model_output_decoder.go, with
// internal/application/scripts/compat/legacy_model_output_decoder.go
// as the cache-fallback path. EngineResult now carries the typed
// script.ModelScriptOutputV1 directly; the deprecated flat
// EngineResult.Script string has been removed.
//
// The Engine does NOT own:
//   - clip context building (ClipSourceBuilder responsibility)
//   - entity extraction / scene images / voiceovers (Pipeline / postprocessor responsibility)
//   - prompt/fingerprint/cache-key derivation (PR 2 responsibility)
package scripts

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/compat"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"

	"go.uber.org/zap"
)

// Engine is the canonical script generation engine backed by
// ollama.Generator, gemmamemory.Service, and ScriptRepository.
// All fields are concrete typed; the ollamaGen field stores a
// scriptOllamaGenerator (narrow interface) so tests can inject
// fakes without depending on the concrete *ollama.Generator.
type Engine struct {
	ollamaGen interface{} // scriptOllamaGenerator
	memorySvc interface{} // memoryGateChecker
	repo      interface{} // ScriptRepository
	log       *zap.Logger
}

// scriptOllamaGenerator is the narrow interface satisfied by both
// *ollama.Generator (production) and fakeOllamaGen (tests).
type scriptOllamaGenerator interface {
	GenerateScript(ctx context.Context, req ollamatypes.TextGenerationRequest) (*ollamatypes.GenerationResult, error)
}

// memoryGateChecker is the narrow interface satisfied by both
// *gemmamemory.Service (production) and fakeMemoryGate (tests).
type memoryGateChecker interface {
	CheckGate(ctx context.Context, req gemmamemory.MemoryGateRequest) (*gemmamemory.GateResult, error)
}

// Compile-time assertions: concrete types satisfy the narrow interfaces.
var _ scriptOllamaGenerator = (*ollama.Generator)(nil)
var _ memoryGateChecker = (*gemmamemory.Service)(nil)

// EngineResult is the canonical typed output of Engine.Generate.
// It carries the canonical ModelScriptOutputV1 produced by the
// model, model metadata, cache outcome, and the resolved clip
// evidence that fed the generation.
//
// PR 1 (June 2026): the deprecated flat `Script string` and `Prompt
// string` fields were removed. Consumers must read `Output.Text`,
// `Output.SpecScene`, and (when present) `Output.SpecScene.Scenes`
// directly. Persistence still occurs inside Engine when
// ResolvedGenerationPlan.SaveToDB is true — this duplication with
// PersistenceProcessor is owned by PR 5, which deletes the
// engine-side SaveScript call entirely.
type EngineResult struct {
	// Output is the canonical structured output. In PR 1 the engine
	// decodes the raw model payload (or the memory-cache hit) into
	// this shape before returning. A nil output indicates an error
	// during decode or generation.
	Output scriptpkg.ModelScriptOutputV1 `json:"output"`

	// WordCount carries the model's reported token count (the engine
	// does not re-derive it; presence is for caller convenience).
	WordCount int `json:"word_count"`

	// Model is the model name that produced the output.
	Model string `json:"model"`

	// CacheStatus is "exact_hit" (memory gate) or "generated".
	CacheStatus string `json:"cache_status"`

	// EstDuration is the script's estimated spoken duration in
	// seconds, computed by the model's output.
	EstDuration int `json:"est_duration"`

	// ScriptID is the persisted script row ID, set when SaveToDB was
	// enabled on the plan AND the engine-side persistence path
	// ran. PR 5 will remove this field; consumers must read
	// postprocessor artifacts in the canonical pipeline.
	ScriptID int64 `json:"script_id,omitempty"`

	// ClipEvidence echoes the resolved clip evidence from the plan
	// so downstream (buildGenerationResult) doesn't re-derive it.
	ClipEvidence *scriptpkg.ClipEvidence `json:"clip_evidence,omitempty"`
}

// NewEngine constructs a real Engine backed by the canonical
// *ollama.Generator. Accepts concrete typed args.
func NewEngine(
	ollamaGen *ollama.Generator,
	memorySvc *gemmamemory.Service,
	repo ScriptRepository,
	log *zap.Logger,
) *Engine {
	return &Engine{
		ollamaGen: ollamaGen,
		memorySvc: memorySvc,
		repo:      repo,
		log:       log,
	}
}

// Generate executes the full generation pipeline for a resolved plan.
// It owns: memory gate check, ollama invocation, and optional DB
// persistence. It is the ONLY engine entry point for new code.
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

	ollamaGen, ok := e.ollamaGen.(scriptOllamaGenerator)
	if !ok || ollamaGen == nil {
		return nil, fmt.Errorf("engine: ollama generator not properly configured")
	}

	// Extract parameters from the resolved plan.
	title := plan.Title
	topic := plan.Topic
	language := plan.Language
	tone := plan.Tone
	model := plan.Model
	mode := plan.Mode
	sourceText := plan.SourceText
	prompt := plan.Prompt
	minWords := plan.TargetWords
	useMemory := plan.UseMemory
	saveToDB := plan.SaveToDB

	// ForceRefresh bypasses the memory gate read.
	skipMemory := plan.ForceRefresh

	if topic == "" {
		topic = title
	}
	if title == "" {
		title = topic
	}
	if language == "" {
		language = "en"
	}
	if tone == "" {
		tone = "documentary"
	}

	if e.log != nil {
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
			zap.Bool("save_to_db", saveToDB))
	}

	// Memory gate: check if we have a cached result.
	// ForceRefresh bypasses the cache read even when UseMemory is true.
	if useMemory && !skipMemory && e.memorySvc != nil {
		if memSvc, ok := e.memorySvc.(memoryGateChecker); ok {
			memoryReq := gemmamemory.MemoryGateRequest{
				Title:    title,
				Language: language,
				Mode:     mode,
			}
			if result, memErr := memSvc.CheckGate(ctx, memoryReq); memErr == nil && result != nil && result.Output != "" {
				if e.log != nil {
					e.log.Info("engine: memory gate cache hit",
						zap.String("title", title),
						zap.Int("word_count", result.WordCount))
				}
				// PR 1: cached output may be canonical V1 JSON, a
				// pre-V1 legacy array, or unparseable prose. The
				// decoder helper tries canonical first, falls back
				// to legacy, and returns ErrModelOutputMalformed on
				// both-fail so callers see a typed poison-cache
				// failure rather than a silent downgrade.
				output, decodeErr := decodeModelPayload([]byte(result.Output), e.log)
				if decodeErr != nil {
					return nil, decodeErr
				}
				return &EngineResult{
					Output:       *output,
					WordCount:    result.WordCount,
					Model:        result.Model,
					CacheStatus:  "exact_hit",
					EstDuration:  (result.WordCount * 60) / 150,
					ClipEvidence: plan.ClipEvidence,
				}, nil
			}
		}
	}

	// Build ollama request from the resolved plan.
	clipIDs := extractPlanClipIDs(plan)

	// PR 1: enforce the canonical V1 output contract on the wire.
	// The decoder will reject any payload that does not match; this
	// prompt suffix steers the model toward emitting canonical JSON.
	// Native Ollama JSON-mode (the adapter-side "format" parameter)
	// is PR 3 territory and is intentionally not wired here.
	builtPrompt := prompt
	if plan.OutputFmt != "prose" {
		builtPrompt += v1OutputInstruction
	}

	ollamaReq := ollamatypes.TextGenerationRequest{
		Language:    language,
		Tone:        tone,
		Model:       model,
		Prompt:      builtPrompt,
		SourceText:  sourceText,
		Title:       title,
		MinWords:    minWords,
		MaxChars:    plan.MaxChars,
		ClipIDs:     clipIDs,
		Temperature: plan.Temperature,
		OutputMode:  ollamatypes.OutputModeScriptV1,
	}

	genResult, err := ollamaGen.GenerateScript(ctx, ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("engine: ollama generation failed: %w", err)
	}

	if e.log != nil {
		e.log.Info("engine: script generated",
			zap.Int("word_count", genResult.WordCount),
			zap.String("model", genResult.Model),
			zap.Int("est_duration_s", genResult.EstDuration))
	}

	// PR 1: decode the raw model payload into the canonical V1 shape.
	// On failure the engine returns a typed script.ErrModelOutputMalformed
	// so callers can surface the failure uniformly with legacy-poison
	// cache hits (see decodeModelPayload below).
	output, decodeErr := DecodeModelOutput([]byte(genResult.Script), e.log)
	if decodeErr != nil {
		return nil, fmt.Errorf("engine: model output decode failed: %w", decodeErr)
	}

	// Persist to DB if requested.
	//
	// PR 5 will consolidate persistence into a dedicated
	// PersistenceProcessor; for now the engine-side SaveScript call
	// remains so PR 1 is wire-up only and SaveToDB semantics are
	// unchanged for downstream consumers. The input is now read
	// from output.Text (canonical) instead of genResult.Script (raw).
	scriptID := int64(0)
	if saveToDB && e.repo != nil && output.Text != "" {
		if repo, ok := e.repo.(ScriptRepository); ok {
			rec := &ScriptRecord{
				Title:          title,
				Topic:          topic,
				Language:       language,
				Tone:           tone,
				Model:          model,
				ModelUsed:      genResult.Model,
				Mode:           mode,
				Status:         "completed",
				TargetWords:    minWords,
				FinalWordCount: genResult.WordCount,
				OutputText:     output.Text,
				NarrativeText:  output.Text,
				FullDocument:   output.Text,
				Version:        1,
			}
			id, saveErr := repo.SaveScript(ctx, rec, nil, nil)
			if saveErr != nil {
				if e.log != nil {
					e.log.Warn("engine: failed to save script to db", zap.Error(saveErr))
				}
			} else {
				scriptID = id
			}
		}
	}

	return &EngineResult{
		Output:       *output,
		WordCount:    genResult.WordCount,
		Model:        genResult.Model,
		CacheStatus:  "generated",
		EstDuration:  genResult.EstDuration,
		ScriptID:     scriptID,
		ClipEvidence: plan.ClipEvidence,
	}, nil
}

// extractPlanClipIDs extracts clip IDs from the resolved plan's
// ClipEvidence. Returns nil for text-only plans (no clip evidence).
func extractPlanClipIDs(plan *scriptpkg.ResolvedGenerationPlan) []string {
	if plan == nil || plan.ClipEvidence == nil {
		return nil
	}
	return plan.ClipEvidence.ClipIDs
}

// decodeModelPayload parses a raw model payload into the canonical
// ModelScriptOutputV1 used by EngineResult.Output. It tries the
// canonical V1 decoder first; on failure it falls back to the
// compat.LegacyArrayToOutput decoder to honour pre-V1 cache rows
// during the migration window. Both-fail is a typed failure wrapping
// scriptpkg.ErrModelOutputMalformed so callers can surface the error
// uniformly with fresh-decode failures.
//
// PR 1 contract: new cache writes MUST emit canonical V1. The legacy
// decoder only handles reads of pre-existing cache entries from
// before the V1 rollout.
func decodeModelPayload(raw []byte, log *zap.Logger) (*scriptpkg.ModelScriptOutputV1, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: decodeModelPayload — empty payload", scriptpkg.ErrModelOutputMalformed)
	}
	if output, err := DecodeModelOutput(raw, log); err == nil {
		return output, nil
	} else if legacy, legacyErr := compat.LegacyArrayToOutput(raw); legacyErr == nil {
		// Canonical decoder failed but the legacy array decoder
		// succeeded — promote the legacy shape to the canonical V1
		// struct so downstream consumers see V1 exclusively.
		return legacy, nil
	} else {
		if log != nil {
			log.Debug("decodeModelPayload: both decoders failed",
				zap.Int("raw_bytes", len(raw)))
		}
		_ = err
		_ = legacyErr
		return nil, fmt.Errorf("%w: decodeModelPayload — both canonical and legacy decoders failed", scriptpkg.ErrModelOutputMalformed)
	}
}

// v1OutputInstruction is the prompt suffix appended when OutputMode
// is OutputModeScriptV1. The decoder tolerates code fences, so the
// instruction explicitly forbids them to bias the model toward
// emitting clean canonical JSON on the wire.
//
// When the adapter-side JSON-mode (the ollama.Generator "format"
// parameter) lands in PR 3, this instruction will be replaced by a
// native format flag and used only as a fallback.
const v1OutputInstruction = `

[OUTPUT_FORMAT]
Respond ONLY with a single JSON object matching the canonical V1 shape:

  {
    "schema_version": 1,
    "text": "<complete script prose>",
    "specscene": {
      "version": 1,
      "scenes": [
        {"id": "scene-N", "index": N, "text": "<scene narration>", "kind": "narration|clip|image|mixed", "bindings": {}}
      ]
    }
  }

Do not include any text outside the JSON object. Do not wrap the JSON in markdown fences. Top-level keys are required: schema_version, text, specscene. SpecScene.scenes requires id (non-empty), index (sequential from 0), text (non-empty), kind (one of narration|clip|image|mixed), bindings (object).`

// Package scripts — engine.go provides the canonical script generation
// engine backed by ollama.Generator, gemmamemory.Service, and
// ScriptRepository.
//
// PG-029 (June 2026): Engine struct consolidated here from the
// now-deleted types.go.
//
// PR 6 (June 2026): canonical Generate(ctx, plan) method — the ONLY
// engine entry point. Accepts a ResolvedGenerationPlan and returns
// a typed EngineResult.
//
// PR 9 (June 2026): stale "future PR" cleanup. The WriteScript removal
// is historical, no longer referenced. The Engine struct body is the
// canonical single source of truth for script generation.
//
// The Engine owns:
//   - ollama script generation (delegates to *ollama.Generator)
//   - memory gate check via gemmamemory (UseMemory path)
//   - payload decoding via jsonextract.Scanner (P0.8)
//
// PR 5 (June 2026): persistence moved out of the engine.
// ScriptRepository is no longer a dependency of Engine — the
// single writer is PersistenceProcessor (registered in the plan's
// Postprocessors list). EngineResult.ScriptID is dropped; consumers
// source the canonical ScriptID from postResult.ScriptID (set by
// PersistenceProcessor). The engine does NOT participate in
// scripts-table persistence (see processor_persistence.go for the
// single-writer contract).
//
// P0.8 (June 2026): payload decoding unified into
// internal/application/scripts/jsonextract/. Engine uses
// ModeStrict on the fresh path (errors on bare prose) and
// ModeCompatibility on the cache-replay path (declared fallback
// with Prometheus metrics).
//
// The Engine does NOT own:
//   - clip context building (ClipSourceBuilder responsibility)
//   - entity extraction / scene images / voiceovers (postprocessor responsibility)
//   - prompt/fingerprint/cache-key derivation (PR 2 responsibility)
//   - script-table persistence (PersistenceProcessor responsibility)
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jsonextract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// Engine is the canonical script generation engine backed by
// ollama.Generator and gemmamemory.Service. All fields are
// concrete typed; the ollamaGen field stores a
// scriptOllamaGenerator (narrow interface) so tests can inject
// fakes without depending on the concrete *ollama.Generator.
//
// PR 5 (June 2026): the `repo ScriptRepository` field was removed.
// Persistence is no longer the engine's responsibility — the
// single owner is PersistenceProcessor, registered in the plan's
// Postprocessors list.
type Engine struct {
	ollamaGen interface{} // scriptOllamaGenerator
	memorySvc interface{} // memoryGateChecker
	log       *zap.Logger
}

// scriptOllamaGenerator is the narrow interface satisfied by both
// *ollama.Generator (production) and fakeOllamaGen (tests).
type scriptOllamaGenerator interface {
	GenerateScript(ctx context.Context, req ollamatypes.TextGenerationRequest) (*ollamatypes.GenerationResult, error)
}

// memoryGateChecker is the narrow interface satisfied by both
// *adapters.Service (production) and fakeMemoryGate (tests).
//
// Phase 1c TODO: the adapters package defines MemoryGateRequest and
// GateResult; defining local copies here means the type assertion
// e.memorySvc.(memoryGateChecker) will always fail against the
// concrete *adapters.Service (Go treats structs from different
// packages as distinct types even with identical fields). Extract
// these types into scripts/contracts/ so both packages share one
// canonical definition.
type memoryGateChecker interface {
	CheckGate(ctx context.Context, req memoryGateRequest) (*memoryGateResult, error)
}

// memoryGateRequest is a local copy of adapters.MemoryGateRequest.
// See Phase 1c TODO on memoryGateChecker.
type memoryGateRequest struct {
	ChannelID    string
	Title        string
	Prompt       string
	Language     string
	Mode         string
	CacheKey     string
	UseMemory    bool
	ForceRefresh bool
}

// memoryGateResult is a local copy of adapters.GateResult.
// See Phase 1c TODO on memoryGateChecker.
type memoryGateResult struct {
	Hit       bool
	Output    string
	WordCount int
	Model     string
}

// Compile-time assertions: concrete types satisfy the narrow interfaces.
var _ scriptOllamaGenerator = (*ollama.Generator)(nil)

var _ memoryGateChecker = (memoryCache)(nil)

// EngineResult is the canonical typed output of Engine.Generate.
// It carries the canonical ModelScriptOutputV1 produced by the
// model, model metadata, cache outcome, and the resolved clip
// evidence that fed the generation.
//
// PR 1 (June 2026): the deprecated flat `Script string` and `Prompt
// string` fields were removed. Consumers must read `Output.Text`,
// `Output.SpecScene`, and (when present) `Output.SpecScene.Scenes`
// directly.
//
// PR 5 (June 2026): `ScriptID int64` removed — persistence is no
// longer owned by the engine. Consumers must source the persisted
// ScriptID from the postprocessor pipeline result
// (PipelineResult.ScriptID, set by PersistenceProcessor when the
// plan's Postprocessors list includes "persistence" and the
// idempotency lookup succeeded).
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

	// ClipEvidence echoes the resolved clip evidence from the plan
	// so downstream (buildGenerationResult) doesn't re-derive it.
	ClipEvidence *scriptpkg.ClipEvidence `json:"clip_evidence,omitempty"`
}

// NewEngine constructs a real Engine backed by the canonical
// *ollama.Generator. Accepts concrete typed args.
//
// PR 5 (June 2026): the `repo ScriptRepository` argument was
// removed. Persistence is owned by PersistenceProcessor; the
// engine does NOT receive a ScriptRepository.
func NewEngine(
	ollamaGen *ollama.Generator,
	memorySvc memoryCache,
	log *zap.Logger,
) *Engine {
	return &Engine{
		ollamaGen: ollamaGen,
		memorySvc: memorySvc,
		log:       log,
	}
}

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

	// ForceRefresh bypasses the memory gate read.
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
			memoryReq := memoryGateRequest{
				Title:    title,
				Language: language,
				Mode:     mode,
				// PR 2: feed the canonical cache key alongside the
				// legacy Title/Language/Mode lookup. The gemmamemory
				// stub still returns nil; production wiring uses
				// CacheKey when the real Service lands.
				CacheKey: cacheKey,
			}
			if result, memErr := memSvc.CheckGate(ctx, memoryReq); memErr == nil && result != nil && result.Output != "" {
				if e.log != nil {
					e.log.Info("engine: memory gate cache hit",
						zap.String("title", title),
						zap.Int("word_count", result.WordCount))
				}
				// P0.8 (June 2026): jsonextract.Scanner in ModeCompatibility —
				// cascading fallback: V1 → legacy array → plain-text wrapper.
				// All fallbacks are declared and measured via Prometheus counters.
				scanner := &jsonextract.Scanner{Mode: jsonextract.ModeCompatibility}
				output, decodeErr := scanner.Scan([]byte(result.Output), "cache")
				if decodeErr != nil {
					return nil, decodeErr
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
					// ScriptID intentionally absent: PR 5 moved
					// persistence to PersistenceProcessor; the cached
					// payload does NOT trigger an extra DB lookup from
					// the engine. The use case resolves ScriptID through
					// the postprocessor pipeline (which sees
					// idem-key collision on replay and returns the
					// existing ID).
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
	// PR 2 + P0.1/P0.2 (June 2026): the model receives RenderedPrompt
	// (editorial body) + the V1 output-format suffix unconditionally.
	//
	// P0.1 flipped the default OutputFmt to the structured V1 contract
	// ("json"), and the validator now rejects the legacy free-form
	// value outright. The legacy per-plan conditional around the
	// suffix is therefore dead code: every canonical entry-point
	// lands here with OutputFmt in the V1 set, so the suffix is
	// always appended.
	//
	// P0.2 additionally sets `format` on the wire to the JSON-mode
	// native flag in generate.go::GenerateScript whenever the
	// V1 contract is requested — but the prompt suffix remains as
	// defense in depth (see v1OutputInstruction's own comment:
	// native json mode guarantees syntactically valid JSON; the
	// suffix enforces the V1 schema shape and forbids prose that
	// would otherwise sneak into the output around the JSON object).
	builtPrompt := renderedPrompt
	if clipRules := buildClipGroundingInstructions(plan); clipRules != "" {
		if builtPrompt != "" {
			builtPrompt += "\n\n"
		}
		builtPrompt += clipRules
	}
	builtPrompt += v1OutputInstruction

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

	// P0.8 (June 2026): jsonextract.Scanner in ModeStrict — no
	// fallbacks. Bare prose and legacy arrays both produce
	// ErrModelOutputMalformed. The cache-replay path uses
	// ModeCompatibility (declared fallback with Prometheus metrics).
	//
	// PR-FIX (June 2026): when ModeStrict rejects the model output
	// (e.g. duplicate scene IDs, missing keys, invalid kinds),
	// retry once with ModeCompatibility — the cascading fallback
	// (V1 → legacy array → plain-text wrapper) salvages the
	// generation instead of failing the entire job. The
	// ModeCompatibility source label is "fresh-fallback" so
	// operators can distinguish true cache-replay fallbacks
	// from retry-after-strict-failure fallbacks.
	scanner := &jsonextract.Scanner{Mode: jsonextract.ModeStrict}
	output, decodeErr := scanner.Scan([]byte(genResult.Script), "fresh")
	if decodeErr != nil {
		if e.log != nil {
			e.log.Warn("engine: ModeStrict decode failed, retrying with ModeCompatibility",
				zap.Error(decodeErr))
		}
		scanner.Mode = jsonextract.ModeCompatibility
		output, decodeErr = scanner.Scan([]byte(genResult.Script), "fresh-fallback")
		if decodeErr != nil {
			return nil, fmt.Errorf("engine: model output decode failed (strict + compatibility): %w", decodeErr)
		}
		if e.log != nil {
			e.log.Info("engine: ModeCompatibility fallback succeeded",
				zap.Int("word_count", genResult.WordCount))
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
	// The engine is the canonical owner of generation (memory gate
	// check, ollama invocation, payload decode) but NOT persistence.
	// When plan.SaveToDB is true, the calling use case must include
	// "persistence" in the plan's Postprocessors list so that
	// PersistenceProcessor is dispatched and writes the script row
	// with idem-key dedup. The engine returns ScriptID = 0 always;
	// consumers must read the canonical ScriptID from the
	// postprocessor pipeline result.
	_ = saveToDB // intentionally unused in the engine post-PR 5.

	return &EngineResult{
		Output:       *output,
		WordCount:    genResult.WordCount,
		Model:        genResult.Model,
		CacheStatus:  "generated",
		EstDuration:  genResult.EstDuration,
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

// buildClipGroundingInstructions adds clip-specific prompt guidance
// when the plan carries clip evidence. The goal is to keep the model
// anchored to the supplied clips instead of drifting into generic
// biography.
func buildClipGroundingInstructions(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil || !plan.HasClips() {
		return ""
	}

	clipIDs := strings.Join(plan.ClipEvidence.ClipIDs, ", ")
	requestedClips := len(plan.ClipEvidence.ClipIDs)
	if plan.NumClips > 0 && plan.NumClips < requestedClips {
		requestedClips = plan.NumClips
	}

	var extra []string
	if plan.NumClips > 0 {
		extra = append(extra, fmt.Sprintf("Use exactly %d clip-driven scenes.", requestedClips))
	}
	if plan.SegmentWords > 0 {
		extra = append(extra, fmt.Sprintf("Aim for about %d words per segment.", plan.SegmentWords))
	}
	if len(plan.SegmentTopics) > 0 {
		topics := make([]string, 0, len(plan.SegmentTopics))
		for i, topic := range plan.SegmentTopics {
			topic = strings.TrimSpace(topic)
			if topic == "" {
				continue
			}
			topics = append(topics, fmt.Sprintf("%d. %s", i+1, topic))
		}
		if len(topics) > 0 {
			extra = append(extra, "Segment topics:\n"+strings.Join(topics, "\n"))
		}
	}

	lines := []string{
		"CLIP-GROUNDED WRITING RULES:",
		"1. Treat the supplied clip evidence as the primary source.",
		"2. Every scene must describe what is happening in the clips: action, movement, setting, objects, reactions, and immediate consequences.",
		"3. Stay anchored to the clip sequence and the listed clip IDs: " + clipIDs + ". Do not drift into generic biography unless it directly explains the clip.",
		"4. If a clip contains multiple beats, narrate those beats in order instead of abstracting them away.",
		"5. Do not invent events, dialogue, or transitions that are not supported by the clip evidence.",
		"6. Keep drive links out of the spoken script; they are reference metadata only.",
	}
	lines = append(lines, extra...)
	return strings.Join(lines, "\n")
}

// v1OutputInstruction is the prompt suffix appended unconditionally
// for the canonical script-generation pipeline.
//
// Two layers of enforcement cooperate to keep the model output on the
// ModelScriptOutputV1 contract:
//
//  1. Native Ollama JSON-mode (generate.go::GenerateScript sets
//     `options["format"] = "json"` when OutputMode == script_v1).
//     Ollama forces the model response to be syntactically valid
//     JSON — but a JSON object is not a V1 script; the schema
//     (schema_version, text, specscene.scenes[…].bindings) is still
//     the model's responsibility.
//
//  2. The v1OutputInstruction suffix below tells the model which
//     keys the V1 contract expects, in what shape, and forbids
//     markdown fences and any prose around the JSON object. The
//     decoder (model_output_decoder.go) tolerates code fences for
//     legacy cache rows, but the suffix biases the model toward
//     emitting clean canonical JSON so the decoder's path is the
//     happy path.
//
// Removing this suffix in favour of "json format only" is not safe:
// native json mode does not enforce schema-shaped JSON.
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

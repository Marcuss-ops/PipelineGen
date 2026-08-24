// Package scripts — engine.go provides the canonical script generation
// engine backed by ollama.Generator, gemmamemory.Service, and
// ScriptRepository.
//
// PG-029 (June 2026): Engine struct consolidated here from the
// now-deleted types.go.
//
// PR 6 (June 2026): canonical Generate(ctx, plan) method — the ONLY
// engine entry point. Accepts a ResolvedGenerationPlan and returns
// a typed EngineResult. Implementation lives in engine_generate.go.
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
// P0.8 (June 2026) + DL-MODECOMPAT-REMOVAL (August 2026): payload decoding
// unified into internal/capabilities/scripts/jsonextract/. Engine uses
// ModeFreshPlainText (sole canonical mode) — V1 JSON fast lane then
// ParsePlainTextFresh (canonical primary path in fresh_parser.go)
// for plain prose per the LLM-PLAIN-TEXT-CONTRACT wave.
// ModeCompatibility was removed August 2026.
//
// The Engine does NOT own:
//   - clip context building (ClipSourceBuilder responsibility)
//   - entity extraction / scene images / voiceovers (postprocessor responsibility)
//   - prompt/fingerprint/cache-key derivation (PR 2 responsibility)
//   - script-table persistence (PersistenceProcessor responsibility)
//
// PR-GENERATE-SPLIT (July 2026): decomposed into 3 files per AGENTS.md
// Pattern 5:
//
//	engine.go           — struct + types + constructor (this file)
//	engine_generate.go  — Generate method (the canonical entry point)
//	engine_prompt.go    — prompt helpers (extractPlanClipIDs,
//	                       buildClipGroundingInstructions, plainTextInstruction)
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// Engine is the canonical script generation engine backed by
// ollama.Generator and gemmamemory.Service. All fields are
// typed with narrow interfaces so tests can inject fakes
// without depending on concrete implementations.
//
// AZIONE 4 (July 2026): ollamaGen and memorySvc fields changed
// from any to the typed narrow interfaces
// scriptOllamaGenerator and memoryGateChecker. Type assertions
// in Generate() removed — the compiler enforces the contract.
//
// PR 5 (June 2026): the `repo ScriptRepository` field was removed.
// Persistence is no longer the engine's responsibility — the
// single owner is PersistenceProcessor, registered in the plan's
// Postprocessors list.
type Engine struct {
	ollamaGen scriptOllamaGenerator
	memorySvc memoryGateChecker
	log       *zap.Logger

	// Segment QA policy is configured at the composition root. Zero values
	// use the canonical defaults in segment_validation.go.
	segmentWordsTolerancePercent   float64
	totalWordsTolerancePercent     float64
	maxSegmentRegenerationAttempts int
	defaultLanguage                string
	defaultTone                    string
	wordsPerMinute                 int
	generationGate                 *scriptgen.GenerationGate
}

// SetGenerationGate limits the individual Ollama invocation. It must not be
// held across the whole script: segment workers need to overlap while still
// sharing one hard ceiling with other local Ollama workloads.
func (e *Engine) SetGenerationGate(gate *scriptgen.GenerationGate) {
	if e != nil {
		e.generationGate = gate
	}
}

// GenerationConcurrency exposes the configured gate ceiling to the streaming
// adapter so it does not create more local jobs than Ollama can admit.
func (e *Engine) GenerationConcurrency() int {
	if e == nil || e.generationGate == nil {
		return 0
	}
	return e.generationGate.Capacity()
}

// scriptOllamaGenerator is the narrow interface satisfied by both
// *ollama.Generator (production) and fakeOllamaGen (tests).
type scriptOllamaGenerator = ports.ScriptGenerator

// memoryGateChecker is the narrow interface satisfied by any future
// gemmamemory adapter exposing CheckGate; the canonical fakeMemoryGate
// type in the test suite also satisfies it directly.
//
// Commit H Phase 2 (June 2026): the gemmamemory gate service +
// MemoryCacheAdapter wrapper were removed from the cross-package
// surface. The engine now passes nil memoryGateChecker to the ctor; the
// runtime `useMemory && !skipMemory && e.memorySvc != nil` check
// short-circuits the cache path. Local types memoryGateRequest +
// memoryGateResult remain as the in-package narrow-type contract.
//
// AZIONE 5 (July 2026): the broader `memoryCache` interface (declared
// in cache_eviction_usecase.go until that file's retirement) and its
// compile-time identity lock are gone. The narrow shape is the
// single canonical contract.
type memoryGateChecker interface {
	CheckGate(ctx context.Context, req memoryGateRequest) (*memoryGateResult, error)
}

// memoryGateRequest: in-package narrow type for the memoryCache interface.
// See Commit H Phase 2 note on memoryGateChecker.
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

// memoryGateResult: in-package narrow type for the memoryCache interface.
// See Commit H Phase 2 note on memoryGateChecker.
type memoryGateResult struct {
	Hit       bool
	Output    string
	WordCount int
	Model     string
}

// memoryGateAdapter bridges the canonical gemmamemory adapter
// (internal/application/scripts/adapters.Service) to the engine's
// in-package memoryGateChecker interface. This keeps the engine's
// narrow contract local while still allowing the real SQLite-backed
// service to be injected from the composition root.
type memoryGateAdapter struct {
	svc *adapters.Service
}

// CheckGate implements memoryGateChecker by forwarding to the canonical
// gemmamemory adapter and translating the request/response shapes.
func (a *memoryGateAdapter) CheckGate(ctx context.Context, req memoryGateRequest) (*memoryGateResult, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	res, err := a.svc.CheckGate(ctx, adapters.MemoryGateRequest{
		ChannelID:    req.ChannelID,
		Title:        req.Title,
		Prompt:       req.Prompt,
		Language:     req.Language,
		Mode:         req.Mode,
		CacheKey:     req.CacheKey,
		UseMemory:    req.UseMemory,
		ForceRefresh: req.ForceRefresh,
	})
	if err != nil || res == nil {
		return nil, err
	}
	return &memoryGateResult{
		Hit:       res.Hit,
		Output:    res.Output,
		WordCount: res.WordCount,
		Model:     res.Model,
	}, nil
}

// NewMemoryGateChecker wraps the canonical gemmamemory service so it
// satisfies the engine's narrow memoryGateChecker interface. A nil
// service returns a checker that always reports a cache miss.
func NewMemoryGateChecker(svc *adapters.Service) memoryGateChecker {
	return &memoryGateAdapter{svc: svc}
}

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
	Output script.ModelScriptOutputV1 `json:"output"`

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
	ClipEvidence  *script.ClipEvidence      `json:"clip_evidence,omitempty"`
	SearchResults []script.SearchResultItem `json:"search_results,omitempty"`
}

// NewEngine constructs a real Engine backed by the canonical
// *ollama.Generator. Accepts typed narrow-interface args so
// tests can inject fakes directly.
//
// AZIONE 4 (July 2026): params changed from concrete types to
// narrow interfaces (scriptOllamaGenerator, memoryGateChecker).
// Call sites pass *ollama.Generator and nil (which satisfy
// the interfaces implicitly).
//
// PR 5 (June 2026): the `repo ScriptRepository` argument was
// removed. Persistence is owned by PersistenceProcessor; the
// engine does NOT receive a ScriptRepository.
func NewEngine(
	ollamaGen scriptOllamaGenerator,
	memorySvc memoryGateChecker,
	log *zap.Logger,
) *Engine {
	return &Engine{
		ollamaGen: ollamaGen,
		memorySvc: memorySvc,
		log:       log,
	}
}

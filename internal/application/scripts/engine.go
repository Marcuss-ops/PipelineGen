// Package scripts — engine.go provides the canonical script generation
// engine backed by ollama.Generator, gemmamemory.Service, and
// ScriptRepository.
//
// AGENT-3 (June 2026): the previous Engine stub accepted all deps as
// interface{} and returned hardcoded placeholder text. The real
// implementation stores typed fields and calls ollama.Generator
// .GenerateScript.
//
// PG-029 (June 2026): Engine struct + WriteScriptRequest consolidated
// here from the now-deleted types.go.
//
// PR 6 (June 2026): canonical Generate(ctx, plan) method added. It
// accepts a ResolvedGenerationPlan directly and returns a typed
// EngineResult. WriteScript is deprecated — new callers MUST use
// Generate. The only permitted caller of Generate is
// GenerateOneUseCase.
//
// The Engine owns:
//   - ollama script generation (delegates to *ollama.Generator)
//   - memory gate check via gemmamemory (UseMemory path)
//   - script persistence via ScriptRepository (SaveToDB path)
//
// The Engine does NOT own:
//   - clip context building (ClipSourceBuilder responsibility)
//   - entity extraction / scene images / voiceovers (Pipeline responsibility)
//   - payload decode (GenerateOneUseCase responsibility)
package scripts

import (
	"context"
	"fmt"

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
// It carries the generated script, model metadata, cache outcome,
// and the resolved clip evidence that fed the generation.
type EngineResult struct {
	Script      string `json:"script"`
	WordCount   int    `json:"word_count"`
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	CacheStatus string `json:"cache_status"` // "exact_hit", "generated"
	EstDuration int    `json:"est_duration"`
	ScriptID    int64  `json:"script_id,omitempty"`

	// ClipEvidence echoes the resolved clip evidence from the plan
	// so downstream (ResultBuilder) doesn't re-derive it.
	ClipEvidence *scriptpkg.ClipEvidence `json:"clip_evidence,omitempty"`
}

// DeprecatedWriteScriptRequest carries the inputs for the deprecated
// WriteScript method. It exists only for compatibility with
// media_curator.go during the migration window.
//
// Deprecated: use Engine.Generate(ctx, plan) instead.
// Removal: PR 11 (legacy adapter cutover) or PR 12 (CONTRACT).
type DeprecatedWriteScriptRequest struct {
	Plan        interface{} // *scriptpkg.ScriptGenerationPlan
	Topic       string
	Title       string
	Language    string
	Tone        string
	Model       string
	Mode        string
	SourceText  string
	MinWords    int
	MaxChars    int
	Prompt      string
	UseMemory   bool
	SaveToDB    bool
	SaveTimeout int
	ClipPack    interface{}
}

// WriteScriptRequest is a backward-compatibility alias for
// DeprecatedWriteScriptRequest.
//
// Deprecated: use Engine.Generate(ctx, plan) instead.
type WriteScriptRequest = DeprecatedWriteScriptRequest

// WriteScriptResult is a backward-compatibility type alias.
// Deprecated: use EngineResult instead.
type WriteScriptResult struct {
	Script      string
	WordCount   int
	Model       string
	Prompt      string
	CacheStatus string
	CacheHit    bool
	WasCached   bool
	EstDuration int
	ScriptID    int64
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
				return &EngineResult{
					Script:       result.Output,
					WordCount:    result.WordCount,
					Model:        result.Model,
					Prompt:       prompt,
					CacheStatus:  "exact_hit",
					EstDuration:  (result.WordCount * 60) / 150,
					ClipEvidence: plan.ClipEvidence,
				}, nil
			}
		}
	}

	// Build ollama request from the resolved plan.
	clipIDs := extractPlanClipIDs(plan)
	ollamaReq := ollamatypes.TextGenerationRequest{
		Language:    language,
		Tone:        tone,
		Model:       model,
		Prompt:      prompt,
		SourceText:  sourceText,
		Title:       title,
		MinWords:    minWords,
		MaxChars:    plan.MaxChars,
		ClipIDs:     clipIDs,
		Temperature: plan.Temperature,
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

	// Persist to DB if requested.
	scriptID := int64(0)
	if saveToDB && e.repo != nil && genResult.Script != "" {
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
				OutputText:     genResult.Script,
				NarrativeText:  genResult.Script,
				FullDocument:   genResult.Script,
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
		Script:       genResult.Script,
		WordCount:    genResult.WordCount,
		Model:        genResult.Model,
		Prompt:       genResult.Prompt,
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

// ── Deprecated compatibility path ─────────────────────────────────

// WriteScript generates a script via ollama.Generator. It converts
// the legacy WriteScriptRequest to a ResolvedGenerationPlan and
// delegates to Generate.
//
// Deprecated: use Engine.Generate(ctx, plan) instead.
// Removal: PR 11 or PR 12 once media_curator.go is migrated.
func (e *Engine) WriteScript(ctx context.Context, req WriteScriptRequest) (*WriteScriptResult, error) {
	// Build a minimal ResolvedGenerationPlan from the legacy request.
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:      req.Title,
		Topic:      req.Topic,
		Language:   req.Language,
		Tone:       req.Tone,
		Model:      req.Model,
		Mode:       req.Mode,
		SourceText: req.SourceText,
		TargetWords: req.MinWords,
		MaxChars:   req.MaxChars,
		Prompt:     req.Prompt,
		UseMemory:  req.UseMemory,
		SaveToDB:   req.SaveToDB,
	}
	if req.Plan != nil {
		if oldPlan, ok := req.Plan.(*scriptpkg.ScriptGenerationPlan); ok {
			plan.Title = oldPlan.Title
			plan.Topic = oldPlan.Topic
			plan.Language = oldPlan.Language
			plan.Tone = oldPlan.Tone
			plan.Model = oldPlan.Model
			plan.Mode = oldPlan.Mode
			plan.SourceText = oldPlan.SourceText
			plan.Prompt = oldPlan.Prompt
			plan.TargetWords = oldPlan.TargetWords
			plan.UseMemory = oldPlan.UseMemory
			plan.SaveToDB = oldPlan.SaveToDB
		}
	}

	// Extract clip evidence from legacy ClipPack.
	if clipIDs := extractClipIDs(req.ClipPack); len(clipIDs) > 0 {
		plan.ClipEvidence = &scriptpkg.ClipEvidence{
			ClipIDs:   clipIDs,
			ClipCount: len(clipIDs),
		}
	}

	result, err := e.Generate(ctx, plan)
	if err != nil {
		return nil, err
	}

	return &WriteScriptResult{
		Script:      result.Script,
		WordCount:   result.WordCount,
		Model:       result.Model,
		Prompt:      result.Prompt,
		CacheStatus: result.CacheStatus,
		CacheHit:    result.CacheStatus == "exact_hit",
		WasCached:   result.CacheStatus == "exact_hit",
		EstDuration: result.EstDuration,
		ScriptID:    result.ScriptID,
	}, nil
}

// extractClipIDs pulls the clip_ids slice from the ClipPack map
// (legacy compatibility path only).
func extractClipIDs(pack interface{}) []string {
	m, ok := pack.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	raw, ok := m["clip_ids"]
	if !ok {
		return nil
	}
	ids, _ := raw.([]string)
	return ids
}

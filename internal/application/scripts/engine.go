// Package scripts — engine.go replaces the Engine stub in types.go with
// a real implementation that delegates to the canonical ollama.Generator.
//
// AGENT-3 (June 2026): the previous Engine stub accepted all deps as
// interface{} and returned hardcoded placeholder text. The real
// implementation stores typed fields and calls ollama.Generator
// .GenerateScript with the parameters extracted from WriteScriptRequest.
//
// The Engine owns:
//   - ollama script generation (delegates to *ollama.Generator)
//   - memory gate check via gemmamemory (UseMemory path)
//   - script persistence via ScriptRepository (SaveToDB path)
//
// The Engine does NOT own:
//   - clip context building (ClipSourceBuilder responsibility)
//   - entity extraction / scene images / voiceovers (Pipeline responsibility)
//   - payload decode (PipelineUseCase responsibility)
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// scriptOllamaGenerator is the narrow GenerateScript surface that
// Engine.WriteScript depends on. The concrete *ollama.Generator satisfies
// it at compile time; tests inject a fake to bypass the real LLM.
type scriptOllamaGenerator interface {
	GenerateScript(ctx context.Context, req ollamatypes.TextGenerationRequest) (*ollamatypes.GenerationResult, error)
}

// memoryGateChecker is the narrow CheckGate surface for the memory-cache
// fast-path in WriteScript. The concrete *gemmamemory.Service satisfies it;
// tests inject a fake to simulate cache hits/misses.
type memoryGateChecker interface {
	CheckGate(ctx context.Context, req gemmamemory.MemoryGateRequest) (*gemmamemory.GateResult, error)
}

// Compile-time assertions.
var _ scriptOllamaGenerator = (*ollama.Generator)(nil)
var _ memoryGateChecker = (*gemmamemory.Service)(nil)

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

// WriteScript generates a script via ollama.Generator.
func (e *Engine) WriteScript(ctx context.Context, req WriteScriptRequest) (*WriteScriptResult, error) {
	if e == nil || e.ollamaGen == nil {
		return nil, fmt.Errorf("engine: ollama generator not configured")
	}

	ollamaGen, ok := e.ollamaGen.(scriptOllamaGenerator)
	if !ok || ollamaGen == nil {
		return nil, fmt.Errorf("engine: ollama generator not properly configured")
	}

	// Resolve parameters: Plan takes precedence when populated.
	plan, _ := req.Plan.(*scriptpkg.ScriptGenerationPlan)

	topic := strings.TrimSpace(req.Topic)
	title := strings.TrimSpace(req.Title)
	language := strings.TrimSpace(req.Language)
	tone := strings.TrimSpace(req.Tone)
	model := strings.TrimSpace(req.Model)
	mode := strings.TrimSpace(req.Mode)
	sourceText := strings.TrimSpace(req.SourceText)
	prompt := strings.TrimSpace(req.Prompt)
	minWords := req.MinWords
	useMemory := req.UseMemory
	saveToDB := req.SaveToDB

	if plan != nil {
		if plan.Title != "" {
			title = plan.Title
		}
		if plan.Topic != "" && topic == "" {
			topic = plan.Topic
		}
		if plan.Language != "" {
			language = plan.Language
		}
		if plan.Tone != "" {
			tone = plan.Tone
		}
		if plan.Model != "" {
			model = plan.Model
		}
		if plan.Mode != "" {
			mode = plan.Mode
		}
		if plan.SourceText != "" {
			sourceText = plan.SourceText
		}
		if plan.Prompt != "" {
			prompt = plan.Prompt
		}
		if plan.TargetWords > 0 {
			minWords = plan.TargetWords
		}
		useMemory = plan.UseMemory
		saveToDB = plan.SaveToDB
	}

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
			zap.Bool("save_to_db", saveToDB))
	}

	// Memory gate: check if we have a cached result.
	if useMemory && e.memorySvc != nil {
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
				return &WriteScriptResult{
					Script:      result.Output,
					WordCount:   result.WordCount,
					Model:       result.Model,
					Prompt:      prompt,
					CacheStatus: "exact_hit",
					CacheHit:    true,
					WasCached:   true,
					EstDuration: (result.WordCount * 60) / 150,
				}, nil
			}
		}
	}

	// Build ollama request.
	ollamaReq := ollamatypes.TextGenerationRequest{
		Language:   language,
		Tone:       tone,
		Model:      model,
		Prompt:     prompt,
		SourceText: sourceText,
		Title:      title,
		MinWords:   minWords,
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

	return &WriteScriptResult{
		Script:      genResult.Script,
		WordCount:   genResult.WordCount,
		Model:       genResult.Model,
		Prompt:      genResult.Prompt,
		CacheStatus: "generated",
		EstDuration: genResult.EstDuration,
		ScriptID:    scriptID,
	}, nil
}

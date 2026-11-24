package gencore

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// ── Generate: the canonical engine entry point ────────────────────────

// ModelSourceText returns the canonical model-facing source projection.
// Clip plans keep their narrative evidence separate from plan.SourceText;
// the engine must send that evidence to the model instead of stale
// request-level source text. A clip-native plan without evidence is
// invalid and must fail closed rather than falling back to stale text.
// Non-clip plans preserve their existing SourceText value.
func ModelSourceText(plan *scriptpkg.ResolvedGenerationPlan) (string, error) {
	if plan == nil {
		return "", nil
	}
	if isClipSourcePlan(plan) {
		if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
			return "", &scriptpkg.PlanInvalidError{
				ItemID:  plan.ID,
				Details: []string{"clip-native plan requires non-empty clip evidence"},
			}
		}
		return strings.TrimSpace(plan.ClipEvidence.ModelSourceText()), nil
	}
	if plan.ResearchEvidence != nil || plan.SourceKind == string(scriptpkg.SourceResearch) {
		if plan.ResearchEvidence == nil {
			return "", &scriptpkg.PlanInvalidError{ItemID: plan.ID, Details: []string{"research plan requires research evidence"}}
		}
		if err := plan.ResearchEvidence.Validate(); err != nil {
			return "", &scriptpkg.PlanInvalidError{ItemID: plan.ID, Details: []string{err.Error()}}
		}
		return strings.TrimSpace(plan.ResearchEvidence.ModelSourceText()), nil
	}
	return strings.TrimSpace(plan.SourceText), nil
}

func isClipSourcePlan(plan *scriptpkg.ResolvedGenerationPlan) bool {
	if plan == nil {
		return false
	}
	return plan.ClipEvidence != nil ||
		plan.SourceKind == string(scriptpkg.SourceClips) ||
		plan.Mode == "clip_to_script" ||
		plan.MediaMode == scriptpkg.MediaModeClipOnly
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
	if plan == nil {
		return nil, fmt.Errorf("engine: plan is nil")
	}
	if e == nil {
		return nil, fmt.Errorf("engine: not configured (receiver is nil)")
	}
	if plan.SourceTextVerbatim {
		return sourceTextVerbatimResult(plan)
	}
	if e.ollamaGen == nil {
		return nil, fmt.Errorf("engine: ollama generator not configured")
	}

	in, err := e.resolveGenerationInputs(plan)
	if err != nil {
		return nil, err
	}
	e.logGenerationDispatch(in)
	if cached, hit, err := e.generationFromMemory(ctx, plan, in); err != nil {
		return nil, err
	} else if hit {
		return cached, nil
	}

	genResult, err := e.generateSegments(ctx, plan, buildTextGenerationRequest(plan, in))
	if err != nil {
		return nil, fmt.Errorf("engine: ollama generation failed: %w", err)
	}
	e.logGenerationSuccess(genResult)
	return e.finalizeGeneratedOutput(plan, genResult)
}

func sourceTextVerbatimResult(plan *scriptpkg.ResolvedGenerationPlan) (*EngineResult, error) {
	if plan.SourceKind != "" && plan.SourceKind != string(scriptpkg.SourceText) && plan.SourceKind != string(scriptpkg.SourceClips) {
		return nil, fmt.Errorf("engine: source_text_verbatim requires source.type=text or clips")
	}
	if len(plan.Segments) == 0 {
		return nil, fmt.Errorf("engine: source_text_verbatim requires explicit script segments")
	}

	scenes := make([]scriptpkg.SpecScene, 0, len(plan.Segments))
	parts := make([]string, 0, len(plan.Segments))
	seen := make(map[string]struct{}, len(plan.Segments))
	for i, segment := range plan.Segments {
		id := strings.TrimSpace(segment.ID)
		if id == "" {
			return nil, fmt.Errorf("engine: source_text_verbatim segment %d has no ID", i)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("engine: source_text_verbatim has duplicate segment ID %q", id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(segment.SourceText) == "" {
			return nil, fmt.Errorf("engine: source_text_verbatim segment %q has no source_text", id)
		}
		scenes = append(scenes, scriptpkg.SpecScene{
			ID: id, SegmentID: id, Index: i, Kind: scriptpkg.SceneNarration,
			Text: segment.SourceText, DurationMS: segment.DurationMS,
			OverlayCategories: scriptpkg.CloneOverlayCategories(segment.OverlayCategories),
		})
		parts = append(parts, segment.SourceText)
	}
	text := strings.Join(parts, "\n\n")
	wordCount := len(strings.Fields(text))
	wordsPerMinute := defaults.DefaultScriptConfig().WordsPerMinute
	if wordsPerMinute <= 0 {
		wordsPerMinute = 150
	}
	output := scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          text,
		SpecScene:     scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
		WordCount:     wordCount,
		ModelUsed:     plan.Model,
		CacheStatus:   "source_text_verbatim",
	}
	return &EngineResult{
		Output: output, WordCount: wordCount, Model: plan.Model,
		CacheStatus:   "source_text_verbatim",
		EstDuration:   (wordCount * 60) / wordsPerMinute,
		ClipEvidence:  plan.ClipEvidence,
		SearchResults: plan.SearchResults,
	}, nil
}

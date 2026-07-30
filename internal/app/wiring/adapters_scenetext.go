// Package app — adapters_scenetext.go implements
// scriptgeneration.TextGenerator by wrapping the canonical
// script-generation Engine.
//
// Verdetto P1: the TextGenerator produces scene text (AI-generated,
// scene-by-scene) SEPARATELY from the Translator. The current code
// does NOT generate text — it only uses script_text already received
// or a local fallback.
//
// This adapter bridges scripgeneration.GenerateRequest (the clean
// domain type) with the existing usecase.Engine (which owns the
// full production-grade prompt construction, ollama invocation,
// output parsing, and sanitization).
//
// Architecture:
//
//	scriptgeneration.TextGenerator
//	         ↑ implements
//	app.SceneTextGenerator
//	         ↑ wraps
//	usecase.Engine → ollama.Generator.GenerateScript
package wiring

import (
	"context"
	"fmt"
	"strings"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/legacy"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// SceneTextGenerator implements scriptgeneration.TextGenerator by
// wrapping the canonical script-generation Engine. Every call to
// GenerateSceneText builds a plan from the request, dispatches the
// engine, and converts the engine's structured output into the
// simple []Scene type expected by the runner.
//
// Panics on nil engine (fail-fast per godlike/07 NO-FAKE-AVAILABILITY).
type SceneTextGenerator struct {
	Engine   *usecase.Engine
	Registry *adapters.SourceRegistry
	Log      *zap.Logger
}

// NewSceneTextGenerator constructs the SceneTextGenerator.
// engine is required; nil panics at construction time.
func NewSceneTextGenerator(engine *usecase.Engine, log *zap.Logger) *SceneTextGenerator {
	if engine == nil {
		panic("app: SceneTextGenerator requires a non-nil engine")
	}
	return &SceneTextGenerator{
		Engine: engine,
		Log:    log,
	}
}

// GenerateSceneText implements scriptgeneration.TextGenerator.
// It converts the GenerateRequest to a plan, calls the engine,
// and converts the engine's SpecScene output to []Scene.
//
// Each returned Scene contains:
//   - ID and Index from the engine's structured output
//   - Text[sourceLanguage] populated with the scene's narrative prose
//   - Clip populated when the engine returned binding data
//
// Returns an error when the engine fails or returns zero scenes.
func (g *SceneTextGenerator) GenerateSceneText(
	ctx context.Context,
	req scriptgen.GenerateRequest,
) ([]scriptgen.Scene, error) {
	plan, err := g.buildPlan(ctx, req)
	if err != nil {
		return nil, err
	}

	if g.Log != nil {
		g.Log.Info("scenetext: generating scene text",
			zap.String("title", plan.Title),
			zap.String("source_type", string(req.Source.Type)),
			zap.String("language", string(req.SourceLanguage)),
		)
	}

	engineResult, err := g.Engine.Generate(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("scenetext: engine generate failed: %w", err)
	}

	scenes := g.convertScenes(engineResult, req.SourceLanguage)
	if len(scenes) == 0 {
		return nil, fmt.Errorf("scenetext: engine returned zero scenes")
	}

	if g.Log != nil {
		g.Log.Info("scenetext: scene text generated",
			zap.Int("scene_count", len(scenes)),
			zap.Int("word_count", engineResult.WordCount),
		)
	}

	return scenes, nil
}

// SetSourceRegistry injects the source registry used to resolve
// non-text sources (clips, catalog, search, curate). The registry is
// built later in the composition root than the SceneTextGenerator,
// so it is supplied via this setter.
func (g *SceneTextGenerator) SetSourceRegistry(reg *adapters.SourceRegistry) {
	g.Registry = reg
}

// buildPlan converts a scriptgeneration.GenerateRequest into a
// ResolvedGenerationPlan consumable by the engine. For text sources
// it assembles the topic/source_text directly. For non-text sources
// it delegates to the injected SourceRegistry to fetch clip evidence
// and the canonical source text.
func (g *SceneTextGenerator) buildPlan(ctx context.Context, req scriptgen.GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
	topic := req.Source.Topic
	if topic == "" && req.Source.SourceText != "" {
		topic = firstLine(req.Source.SourceText)
	}
	title := req.Title
	if title == "" {
		title = topic
		if title == "" {
			title = "Untitled Script"
		}
	}

	renderedPrompt := buildEditorialPromptFromGenReq(req)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             req.IdempotencyKey,
		Title:          title,
		Topic:          topic,
		Language:       string(req.SourceLanguage),
		SourceText:     req.Source.SourceText,
		RenderedPrompt: renderedPrompt,
		Mode:           modeForSourceType(req.Source.Type),
		SourceKind:     string(req.Source.Type),
		// Postprocessors left empty — the engine generates raw
		// narrative prose. Downstream phases handle translation,
		// voiceover, and docs.
	}

	// Non-text sources require source resolution to populate clip
	// evidence and the canonical source text.
	if req.Source.Type != scriptgen.SourceText {
		if g.Registry == nil {
			return nil, fmt.Errorf("scenetext: source registry not configured for source type %q", req.Source.Type)
		}
		resolved, err := g.Registry.Resolve(ctx, genSourceToSourceSpec(req.Source), genRequestToResolutionContext(req))
		if err != nil {
			return nil, fmt.Errorf("scenetext: source resolution failed for type %q: %w", req.Source.Type, err)
		}
		if resolved.Topic != "" {
			plan.Topic = resolved.Topic
		}
		if resolved.Title != "" {
			plan.Title = resolved.Title
		}
		if resolved.SourceText != "" {
			plan.SourceText = resolved.SourceText
		}
		if resolved.ClipEvidence != nil {
			plan.ClipEvidence = resolved.ClipEvidence
		}
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
		// Re-render the prompt from the resolved source text so the
		// engine actually sees the clip evidence.
		plan.RenderedPrompt = buildEditorialPrompt(plan.Topic, plan.SourceText, req.Title, req.Source.Query)
	}

	return plan, nil
}

// convertScenes maps the engine's structured SpecScene output into
// the simple Scene type consumed by the scriptgeneration runner.
func (g *SceneTextGenerator) convertScenes(
	result *usecase.EngineResult,
	sourceLang scriptgen.Language,
) []scriptgen.Scene {
	scenes := make([]scriptgen.Scene, 0, len(result.Output.SpecScene.Scenes))
	for _, s := range result.Output.SpecScene.Scenes {
		scene := scriptgen.Scene{
			ID:    s.ID,
			Index: s.Index,
			Text: map[scriptgen.Language]string{
				sourceLang: s.Text,
			},
		}
		// Populate Clip reference when the engine bound one.
		if s.Bindings.Clip != nil && s.Bindings.Clip.ClipID != "" {
			scene.Clip = &scriptgen.ClipReference{
				ID:    s.Bindings.Clip.ClipID,
				Title: s.Bindings.Clip.ClipTitle,
			}
		}
		scenes = append(scenes, scene)
	}
	return scenes
}

// buildEditorialPromptFromGenReq builds the editorial prompt string
// from a GenerateRequest — specifically the source text assembly that
// tells the model what to write about.
func buildEditorialPromptFromGenReq(req scriptgen.GenerateRequest) string {
	return buildEditorialPrompt(req.Source.Topic, req.Source.SourceText, req.Title, req.Source.Query)
}

// buildEditorialPrompt builds the editorial prompt string from the
// resolved source fields. It is used both for direct text sources
// and for sources that have been resolved through the SourceRegistry.
func buildEditorialPrompt(topic, sourceText, title, query string) string {
	var parts []string
	if topic != "" {
		parts = append(parts, "Topic: "+topic)
	}
	if sourceText != "" {
		parts = append(parts, "Source text:\n"+sourceText)
	}
	if title != "" {
		parts = append(parts, "Title: "+title)
	}
	if query != "" {
		parts = append(parts, "Search query: "+query)
	}
	// The engine prompt builder appends plainTextInstruction
	// at generation time — we don't need to repeat it here.
	parts = append(parts, "Do not include raw URLs, hyperlinks, or source citations in the prose output.")
	return strings.Join(parts, "\n\n")
}

// genSourceToSourceSpec maps the scriptgeneration.Source (the
// pipeline's small source value) to the domain SourceSpec consumed
// by the source registry.
func genSourceToSourceSpec(src scriptgen.Source) scriptpkg.SourceSpec {
	return scriptpkg.SourceSpec{
		Type:       scriptpkg.SourceType(src.Type),
		Topic:      src.Topic,
		SourceText: src.SourceText,
		ClipIDs:    copyStrings(src.ClipIDs),
		Query:      src.Query,
		MaxClips:   src.MaxClips,
	}
}

// genRequestToResolutionContext maps the scriptgeneration request to
// the domain SourceResolutionContext consumed by the source registry.
func genRequestToResolutionContext(req scriptgen.GenerateRequest) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		Title:    req.Title,
		Language: string(req.SourceLanguage),
	}
}

// copyStrings returns a copy of the string slice (nil-safe).
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// modeForSourceType maps scriptgeneration source types to the engine's
// mode strings. Text sources produce "text" mode; clip-based sources
// produce "clip_to_script".
func modeForSourceType(st scriptgen.SourceType) string {
	switch st {
	case scriptgen.SourceText:
		return "text"
	case scriptgen.SourceClips, scriptgen.SourceCatalog, scriptgen.SourceSearch, scriptgen.SourceCurate:
		return "clip_to_script"
	default:
		return "text"
	}
}

// firstLine returns the first line of a multi-line string.
func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}

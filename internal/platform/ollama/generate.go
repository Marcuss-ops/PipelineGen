package ollama

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

	"go.uber.org/zap"
)

// randomSeed returns a non-negative pseudo-random int suitable for Ollama's
// "seed" generation option. We use math/rand (not crypto/rand) because
// reproducibility/determinism are not security properties here — we just
// need a value that changes between calls.
var randomSeed = func() int {
	return int(rand.Int63())
}

type Generator struct {
	client           *client.Client
	translationCache TranslationCache
	metadataModel    string // lighter model for entity extraction, metadata, translations
}

func NewGenerator(c *client.Client) *Generator {
	return &Generator{client: c}
}

func (g *Generator) GetClient() *client.Client {
	return g.client
}

// SetMetadataModel sets a lighter model for post-generation phases
// (entity extraction, video metadata, translations).
func (g *Generator) SetMetadataModel(model string) {
	g.metadataModel = model
}

// resolveModel returns the effective model: explicit > g.metadataModel > client default.
func (g *Generator) resolveModel(model string) string {
	if model != "" {
		return model
	}
	return g.metadataModel
}

func (g *Generator) GenerateDescription(ctx context.Context, mediaType, prompt, style string) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	var systemPrompt, userPrompt string
	if cfg := prompts.Get(); cfg != nil {
		s, u, err := cfg.RenderDescription(mediaType, prompt, style)
		if err == nil {
			systemPrompt, userPrompt = s, u
		}
	}
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant that writes concise, 2-line human-like semantic descriptions for AI generated media assets."
		userPrompt = fmt.Sprintf("Write a 2-line semantic description for a generated %s.\nPROMPT: %s\nSTYLE: %s\n\nRULES:\n1. Be descriptive and natural.\n2. Do NOT use technical terms like 'AI generated' or model names.\n3. Focus on what is seen and the mood.\n4. Return ONLY the 2 lines of description.", mediaType, prompt, style)
	}

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := g.client.Chat(ctx, messages, nil, nil)
	if err != nil {
		return "", fmt.Errorf("description generation failed: %w", err)
	}

	return strings.TrimSpace(result), nil
}

// GenerateVisualPrompt takes a narrative block and turns it into a concise visual prompt for image generation models.
func (g *Generator) GenerateVisualPrompt(ctx context.Context, text, topic, style string) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	var systemPrompt, userPrompt string
	if cfg := prompts.Get(); cfg != nil {
		s, u, err := cfg.RenderVisualPrompt(text, topic, style)
		if err == nil {
			systemPrompt, userPrompt = s, u
		}
	}
	if systemPrompt == "" {
		systemPrompt = "You are a visual design assistant. Convert narrative script blocks into concise, concrete visual prompts for image generation models."
		userPrompt = fmt.Sprintf("Convert this narrative script block into a single concrete visual description for an image generator (like FLUX).\nSCRIPT BLOCK: %s\nTOPIC: %s\nSTYLE: %s\n\nRULES:\n1. Describe only what is visually seen in a single scene.\n2. Include mood, lighting, and camera angle.\n3. Do NOT include narrative text, subtitles, or time markers.\n4. Keep it concise (1-2 sentences maximum).\n5. Return ONLY the visual prompt.", text, topic, style)
	}

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := g.client.Chat(ctx, messages, nil, nil)
	if err != nil {
		return "", fmt.Errorf("visual prompt generation failed: %w", err)
	}

	return strings.TrimSpace(result), nil
}

func (g *Generator) GenerateScript(ctx context.Context, req types.TextGenerationRequest) (*types.GenerationResult, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	setTextDefaults(&req)

	modelLabel := req.Model
	if modelLabel == "" {
		modelLabel = "unknown"
	}
	languageLabel := req.Language
	if languageLabel == "" {
		languageLabel = "unknown"
	}
	genOutcome := "error"
	defer func() {
		metrics.ScriptGenerationTotal.WithLabelValues(modelLabel, languageLabel, genOutcome).Inc()
	}()

	// Auto-retrieve web context if SearXNG is configured and query is derivable
	if ws := g.client.WebSearcher(); ws != nil && !req.DisableWebSearch && req.WebContext == "" {
		searchQuery := SearchQueryForScript(req)
		if searchQuery != "" {
			searchStart := time.Now()
			results, err := ws.Search(ctx, searchQuery)
			if err != nil {
				logger.Warn("web search failed in GenerateScript", zap.Error(err), zap.String("query", searchQuery))
			} else if len(results) > 0 {
				req.WebContext = client.FormatContext(results)
				logger.Info("web search context injected into prompt",
					zap.String("query", searchQuery),
					zap.Int("results", len(results)),
					zap.Duration("elapsed", time.Since(searchStart)),
				)
			}
		}
	}

	messages := prompts.BuildChatMessages(&req)

	// Ensure sensible generation options when caller provides none. Seed is
	// randomized per call when zero so repeated runs on the same prompt do
	// not collapse to the same wording — Ollama gemma models are otherwise
	// highly deterministic at temperature=0.35.
	options := req.Options
	if options == nil {
		options = make(map[string]any)
	}
	// The request-level model is part of the canonical generation plan. The
	// Ollama client accepts per-call model selection through options; without
	// forwarding it, every explicit item model was silently replaced by the
	// process-wide OLLAMA_MODEL, making controlled runs and retries use the
	// wrong model.
	if strings.TrimSpace(req.Model) != "" {
		options["model"] = strings.TrimSpace(req.Model)
	}
	if req.MaxChars > 0 {
		// Gemma4 needs a generous token budget: the model "thinks" first,
		// consuming tokens before the actual response. Budget = JSON structure
		// overhead (256) + per-clip thinking overhead (512) + char limit ÷ 4.
		clipCount := len(req.ClipIDs)
		if clipCount == 0 {
			clipCount = strings.Count(req.SourceText, "NARRATIVE EVIDENCE ")
		}
		perClipOverhead := 512 * max(1, clipCount)
		options["num_predict"] = 256 + perClipOverhead + (req.MaxChars / 4)
	} else if _, ok := options["num_predict"]; !ok {
		options["num_predict"] = types.DefaultNumPredict
	}
	// num_ctx must fit the prompt plus the generation budget. Ollama's
	// default window is 4096 tokens; the research path's editorial prompt
	// embeds the full resolved source text and reaches ~5k tokens, so at the
	// default the model is left with a single output token and fails
	// min_words. Callers may override via req.Options["num_ctx"].
	if _, ok := options["num_ctx"]; !ok {
		options["num_ctx"] = types.DefaultNumCtx
	}
	if _, ok := options["temperature"]; !ok {
		options["temperature"] = types.DefaultTemperature
	}
	if _, ok := options["top_p"]; !ok {
		options["top_p"] = types.DefaultTopP
	}
	if !req.NoSeed {
		if _, ok := options["seed"]; !ok {
			if req.Seed == 0 {
				options["seed"] = randomSeed()
			} else {
				options["seed"] = req.Seed
			}
		}
	} else {
		delete(options, "seed")
	}
	if req.Temperature > 0 {
		options["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		options["top_p"] = req.TopP
	}

	// PR-3 (LLM-PLAIN-TEXT-CONTRACT wave, July 2026): the inline
	// JSON-mode trigger collapsed into resolveGenerationFormat.
	// See generate_format.go for the canonical 4-case decision tree.
	req.Format = resolveGenerationFormat(req)

	// The Ollama client is the owner of the inference boundary, so it emits
	// the canonical ollama/generate operation itself (MeasureOperation). The
	// legacy Prometheus histogram above remains the migration projection;
	// no caller re-times the same request independently. A parallel fan-out
	// binds its provenance (segment_id, worker_id, queued_at) to ctx via
	// WithOperationMeta; it is merged onto this single canonical operation so
	// the fan-out can be reconstructed without a second timer.
	info := kernobs.OperationInfo{
		Stage:     kernobs.StageGenerate,
		Component: kernobs.ComponentOllama,
		Operation: kernobs.OperationGenerate,
		Items:     1,
	}
	if meta, ok := kernobs.OperationMetaFromContext(ctx); ok {
		meta.Apply(&info)
	}
	var result string
	err := kernobs.MeasureOperation(ctx, info, func(ctx context.Context) error {
		out, chatErr := g.client.Chat(ctx, messages, options, req.Format)
		result = out
		return chatErr
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}
	genOutcome = "success"
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:           result,
		WordCount:        wordCount,
		EstDuration:      estimateDurationSecondsWithWPM(wordCount, req.WordsPerMinute),
		Model:            req.Model,
		Prompt:           prompts.BuildTextPrompt(&req),
		GenerationSource: "ollama",
	}, nil
}

func (g *Generator) RegenerateScript(ctx context.Context, req types.RegenerationRequest) (*types.GenerationResult, error) {
	if g.client == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	types.ApplyDefaultsToRegeneration(&req)
	messages := prompts.BuildRegenerationChatMessages(&req)
	result, err := g.client.Chat(ctx, messages, req.Options, nil)
	if err != nil {
		return nil, fmt.Errorf("script regeneration failed: %w", err)
	}
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:      result,
		WordCount:   wordCount,
		EstDuration: estimateDurationSecondsWithWPM(wordCount, 0),
		Model:       req.Model,
		Prompt:      req.OriginalScript,
	}, nil
}

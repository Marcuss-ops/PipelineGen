package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"

	"go.uber.org/zap"
)

// randomSeed returns a non-negative pseudo-random int suitable for Ollama's
// "seed" generation option. We use math/rand (not crypto/rand) because
// reproducibility/determinism are not security properties here — we just
// need a value that changes between calls.
var randomSeed = func() int {
	return int(rand.Int63())
}

type TranslationCache interface {
	Get(ctx context.Context, text, targetLanguage string) (string, bool)
	Set(ctx context.Context, text, targetLanguage, translated string) error
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
	genStart := time.Now()
	genOutcome := "error"
	defer func() {
		metrics.ScriptGenerationDuration.WithLabelValues(modelLabel, languageLabel, genOutcome).Observe(time.Since(genStart).Seconds())
		metrics.ScriptGenerationTotal.WithLabelValues(modelLabel, languageLabel, genOutcome).Inc()
	}()

	// Auto-retrieve web context if SearXNG is configured and query is derivable
	if ws := g.client.WebSearcher(); ws != nil && req.WebContext == "" {
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
	if req.MaxChars > 0 {
		// Gemma4 needs a generous token budget: the model "thinks" first,
		// consuming tokens before the actual response. Budget = JSON structure
		// overhead (256) + per-clip thinking overhead (512) + char limit ÷ 4.
		perClipOverhead := 512 * max(1, len(req.ClipIDs))
		options["num_predict"] = 256 + perClipOverhead + (req.MaxChars / 4)
	} else if _, ok := options["num_predict"]; !ok {
		options["num_predict"] = types.DefaultNumPredict
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

	// P0.2 (June 2026): when the caller requests the structured
	// V1 script contract (OutputModeScriptV1), force Ollama into
	// native JSON-mode so the model response is constrained to
	// syntactically valid JSON.
	//
	// Format is a TOP-LEVEL body field on `/api/chat`. Putting it
	// in `options` would be silently wrong because Ollama treats
	// `options`-nested keys as model parameters (temperature, seed…)
	// — a `format` there is ignored. See types.ChatRequest.Format
	// + client_core.go::doChatRequest for the wire wiring.
	//
	// PR-3 (LLM-PLAIN-TEXT-CONTRACT wave, July 2026): the inline
	// JSON-mode trigger collapsed into resolveGenerationFormat. The
	// pure helper takes the request by value (Go value semantics
	// structurally prevents mutation; see
	// TestResolveGenerationFormat_NoInputMutation) and returns the
	// canonical Ollama Format value per the 4-case decision tree
	// documented there. Canonical PlainText path returns nil (no
	// JSON constraint); legacy ScriptV1 path with empty caller
	// Format auto-fills "json"; caller-supplied Format always wins.
	req.Format = resolveGenerationFormat(req)

	result, err := g.client.Chat(ctx, messages, options, req.Format)
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}
	genOutcome = "success"
	wordCount := len(strings.Fields(result))
	return &types.GenerationResult{
		Script:      result,
		WordCount:   wordCount,
		EstDuration: estimateDurationSeconds(wordCount),
		Model:       req.Model,
		Prompt:      prompts.BuildTextPrompt(&req),
	}, nil
}

// resolveGenerationFormat returns the canonical Ollama wire-shape Format
// value for a text-generation request.
//
// LLM-PLAIN-TEXT-CONTRACT wave (PR-3, July 2026): this pure helper
// replaces an inline conditional that was previously hard-coded in
// (*Generator).GenerateScript. The 3 logical branches implement the
// 4-case decision tree (2 OutputMode values × 2 Format presence values):
//
//  1. OutputMode != OutputModeScriptV1 (PlainText or empty):
//     return nil — Ollama stays in prose mode, no JSON constraint.
//     This is the canonical post-PR-2 path for the script pipeline:
//     engine_generate.go sets OutputModePlainText unconditionally,
//     and downstream SceneSynthesizer + scene binder derive structured
//     fields from the raw prose (no JSON envelope on the wire).
//
//  2. OutputMode == OutputModeScriptV1 AND caller-supplied Format empty:
//     return json.RawMessage(`"json"`) — force Ollama into native
//     JSON-mode so the model response is constrained to syntactically
//     valid JSON. This is the legacy defence for deprecated active
//     callers that still pass OutputModeScriptV1 in their current
//     request payload. Pre-wave callers were never updated to
//     PlainText; without this fallback the LLM emits prose and
//     downstream jsonextract decoders immediately raise
//     ErrModelOutputMalformed on the model output.
//     (Note: cached pre-wave rows skip GenerateScript entirely via
//     TranslateText.* cache fast-path; this fallback does NOT defend
//     cache hits, only the active GenerateScript call surface.)
//
//  3. OutputMode == OutputModeScriptV1 AND caller-supplied Format
//     non-empty (cases 4 of the 2x2 grid):
//     return req.Format verbatim (passthrough). Test rigs and future
//     non-script callers opt out of the auto-fill by pre-setting Format.
//
// Native json-mode does NOT enforce a schema — the plainTextInstruction
// prompt suffix does that (see engine_prompt.go). The wire-format
// trigger here is the FIRST half of the V1 contract defence.
//
// Format is a TOP-LEVEL body field on Ollama's `/api/chat` endpoint —
// it is NOT inside `options` (where Ollama would silently ignore it as
// a non-model parameter). See types.ChatRequest.Format and
// client_core.go::doChatRequest for the canonical wire wiring.
//
// Pure function (value parameter via Go semantics). A future refactor
// that flips to *types.TextGenerationRequest and mutates req would
// surface above the call site because req.Format is aliased both ways
// (the helper itself would still compile, but the caller-observable
// effect would leak). TestResolveGenerationFormat_NoInputMutation is
// the observable per-call guarantee.
func resolveGenerationFormat(req types.TextGenerationRequest) json.RawMessage {
	if len(req.Format) > 0 {
		return req.Format
	}
	if req.OutputMode == types.OutputModeScriptV1 {
		return json.RawMessage(`"json"`)
	}
	return nil
}

// estimateDurationSeconds estimates speech duration from word count using WordsPerMinute (140 WPM)
func estimateDurationSeconds(wordCount int) int {
	if wordCount <= 0 {
		return 0
	}
	return (wordCount * 60) / types.WordsPerMinute
}

func setTextDefaults(req *types.TextGenerationRequest) {
	types.ApplyDefaults(req)
}

// SearchQueryForScript builds a web search query from the script request.
// Returns empty string if the request doesn't benefit from web search
// (e.g. when the source text is substantial enough on its own).
func SearchQueryForScript(req types.TextGenerationRequest) string {
	// Skip web search if the source text is substantial (user already provided context)
	if len(strings.TrimSpace(req.SourceText)) > 500 {
		return ""
	}

	// Use the title/topic as the primary search query
	query := strings.TrimSpace(req.Title)
	if query == "" {
		query = strings.TrimSpace(req.Prompt)
	}
	if query == "" {
		q := strings.TrimSpace(req.SourceText)
		if len(q) > 200 {
			q = q[:200]
		}
		query = q
	}
	if query == "" {
		return ""
	}

	return client.SearchQueryFromTopic(query)
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
		EstDuration: estimateDurationSeconds(wordCount),
		Model:       req.Model,
		Prompt:      req.OriginalScript,
	}, nil
}

// languageNames maps ISO 639-1 codes to full language names for the LLM prompt.
// Using short codes like "it" is ambiguous ("Translate this text to it") and the
// LLM often ignores them, defaulting to Spanish. Full names disambiguate.
var languageNames = map[string]string{
	"en": "English", "it": "Italian", "es": "Spanish",
	"fr": "French", "de": "German", "pt": "Portuguese",
	"nl": "Dutch", "pl": "Polish", "ru": "Russian",
	"ja": "Japanese", "ko": "Korean", "zh": "Chinese",
	"ar": "Arabic", "tr": "Turkish", "sv": "Swedish",
	"da": "Danish", "fi": "Finnish", "no": "Norwegian",
	"cs": "Czech", "hu": "Hungarian", "ro": "Romanian",
	"el": "Greek", "he": "Hebrew", "th": "Thai",
	"vi": "Vietnamese", "id": "Indonesian", "ms": "Malay",
	"uk": "Ukrainian", "hr": "Croatian", "sr": "Serbian",
	"bg": "Bulgarian", "sk": "Slovak", "sl": "Slovenian",
	"lt": "Lithuanian", "lv": "Latvian", "et": "Estonian",
	"ca": "Catalan", "gl": "Galician", "eu": "Basque",
}

// translateLanguageName converts an ISO 639-1 code to a full language name.
// Returns the original string if no mapping exists.
func translateLanguageName(code string) string {
	if name, ok := languageNames[strings.ToLower(strings.TrimSpace(code))]; ok {
		return name
	}
	return code
}

// TranslateText translates text using the Generator's model (or metadataModel if set).
// The model override is passed via options["model"] which Client.Chat supports.
func (g *Generator) TranslateText(ctx context.Context, text, targetLanguage string) (string, error) {
	return g.TranslateTextWithModel(ctx, text, targetLanguage, "")
}

// TranslateTextWithModel translates text using the specified model.
// If model is empty, falls back to g.metadataModel, then the default client model.
func (g *Generator) TranslateTextWithModel(ctx context.Context, text, targetLanguage, model string) (string, error) {
	if g.client == nil {
		return "", fmt.Errorf("ollama client not initialized")
	}

	// ── Translation Cache: check L1 (memory) + L2 (SQLite) ──
	if g.translationCache != nil {
		if cached, ok := g.translationCache.Get(ctx, text, targetLanguage); ok {
			logger.Info("translation cache HIT",
				zap.String("lang", targetLanguage),
				zap.Int("text_len", len(text)),
			)
			return cached, nil
		}
	}

	// Calculate a generous num_predict based on source text length so the
	// model cannot ramble indefinitely and produce philosophical essays
	// instead of faithful translations. 1 char ≈ 0.25 tokens on average;
	// we allow 4x the char length as token budget to handle verbose languages
	// like German, capped at 4096 to avoid runaway generation.
	sourceLen := len([]rune(text))
	predictLimit := sourceLen * 4
	if predictLimit < 512 {
		predictLimit = 512 // minimum for very short texts
	}
	if predictLimit > 4096 {
		predictLimit = 4096 // hard cap — no essay-writing
	}

	// Use full language name in the prompt to avoid LLM ambiguity with short codes.
	// e.g. "it" is confused with the English pronoun "it" → LLM defaults to Spanish.
	langName := translateLanguageName(targetLanguage)

	var systemPrompt, userPrompt string
	if cfg := prompts.Get(); cfg != nil {
		s, u, err := cfg.RenderTranslation(text, targetLanguage)
		if err == nil {
			systemPrompt, userPrompt = s, u
		}
	}
	if systemPrompt == "" {
		systemPrompt = "You are a professional translator. CRITICAL RULES: 1. Translate the text LITERALLY — do NOT expand, explain, philosophize, or add any content. 2. Return ONLY the translated text — no intros, no conclusions, no meta-commentary. 3. Preserve the original structure, paragraphs, lists, and formatting. 4. If you don't know a word, keep it in the original language rather than guessing. 5. Do NOT write essays, do NOT generate philosophical analysis."
		userPrompt = fmt.Sprintf("Translate this text to %s faithfully. No additions, no explanations, no creative writing.\n\nTEXT TO TRANSLATE:\n%s", langName, text)
	}

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	options := map[string]any{
		"num_predict": predictLimit,
		"temperature": 0.1, // low temperature for faithful translation
	}
	// Pass model override to Chat if a metadata model is configured.
	if effectiveModel := g.resolveModel(model); effectiveModel != "" {
		options["model"] = effectiveModel
	}

	result, err := g.client.Chat(ctx, messages, options, nil)
	if err != nil {
		return "", fmt.Errorf("translation failed: %w", err)
	}

	translated := strings.TrimSpace(result)

	// ── Translation Cache: store result ──
	if g.translationCache != nil && translated != "" {
		if storeErr := g.translationCache.Set(ctx, text, targetLanguage, translated); storeErr != nil {
			logger.Warn("failed to store translation in cache", zap.Error(storeErr))
		} else {
			logger.Info("translation cache STORE",
				zap.String("lang", targetLanguage),
				zap.Int("text_len", len(text)),
			)
		}
	}

	return translated, nil
}

// SetTranslationCache attaches a translation cache to the Generator.
// All subsequent TranslateText calls will check the cache first.
func (g *Generator) SetTranslationCache(cache TranslationCache) {
	g.translationCache = cache
}

// GenerateVideoMetadata generates YouTube metadata using the Generator's default model.
// To use a lighter model, call GenerateVideoMetadataWithModel.
func (g *Generator) GenerateVideoMetadata(ctx context.Context, title string) (string, []string, error) {
	return g.GenerateVideoMetadataWithModel(ctx, title, "")
}

// GenerateVideoMetadataWithModel generates YouTube metadata using the specified model.
// If model is empty, falls back to g.metadataModel, then the Generator's default model.
func (g *Generator) GenerateVideoMetadataWithModel(ctx context.Context, title string, model string) (string, []string, error) {
	if g.client == nil {
		return "", nil, fmt.Errorf("ollama client not initialized")
	}

	var systemPrompt, userPrompt string
	if cfg := prompts.Get(); cfg != nil {
		s, u, err := cfg.RenderVideoMetadata(title)
		if err == nil {
			systemPrompt, userPrompt = s, u
		}
	}
	if systemPrompt == "" {
		systemPrompt = "You are a professional video optimizer. Provide metadata strictly in English based on the given title."
		userPrompt = fmt.Sprintf(`Given the video title: "%s"

Generate:
1. A concise, professional, engaging video description (1 to 2 lines max) in English. Do not write intros or greetings, start directly.
2. A list of 5 to 8 generic keywords/tags in English relevant to the topic.

You must respond ONLY with a raw JSON object matching the following structure:
{
  "description": "Engaging description of the video...",
  "tags": ["tag1", "tag2", "tag3"]
}`, title)
	}

	messages := []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	opts := map[string]any{}
	if effectiveModel := g.resolveModel(model); effectiveModel != "" {
		opts["model"] = effectiveModel
	}
	result, err := g.client.Chat(ctx, messages, opts, nil)
	if err != nil {
		return "", nil, fmt.Errorf("metadata generation failed: %w", err)
	}

	// Clean code blocks or extra text if any, and parse the json
	cleanJSON := result
	if idx := strings.Index(cleanJSON, "{"); idx != -1 {
		cleanJSON = cleanJSON[idx:]
	}
	if idx := strings.LastIndex(cleanJSON, "}"); idx != -1 {
		cleanJSON = cleanJSON[:idx+1]
	}

	type MetadataResponse struct {
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}

	var meta MetadataResponse
	if err := json.Unmarshal([]byte(cleanJSON), &meta); err != nil {
		// Fallback parse logic if LLM failed to return valid JSON
		return strings.TrimSpace(result), []string{}, nil
	}

	return meta.Description, meta.Tags, nil
}

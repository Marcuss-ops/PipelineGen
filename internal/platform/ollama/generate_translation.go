package ollama

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"

	"go.uber.org/zap"
)

type TranslationCache interface {
	Get(ctx context.Context, text, targetLanguage string) (string, bool)
	Set(ctx context.Context, text, targetLanguage, translated string) error
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
				zap.String("lang", targetLanguage), zap.Int("text_len", len(text)),
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
		// Use the full language name in the configured prompt as well as
		// in the fallback prompt. Short codes such as "es" are valid
		// metadata, but are less explicit to the model than "Spanish".
		s, u, err := cfg.RenderTranslation(text, langName)
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
				zap.String("lang", targetLanguage), zap.Int("text_len", len(text)),
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

package ollama

import (
	"context"
	"fmt"
	"strings"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
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
	"nl": "Dutch", "pl": "Polish", "ru": "Russian", "pt-br": "Brazilian Portuguese",
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

// Translation output-budget bounds.
//
// minTranslationPredictTokens is the floor for a cue-sized text: a 30-char
// subtitle cue needs ~10 output tokens, so 96 is already ~10x headroom. The old
// floor was 512 and it was NOT headroom, it was cost — measured on a 5-word
// cue, gemma4:e2b generated 294-497 tokens (it used the whole budget every
// time), which on a 10-language subtitle track is the dominant per-cue cost.
const (
	minTranslationPredictTokens = 96
	maxTranslationPredictTokens = 4096
)

// translationOutputBudget converts a source text length into the num_predict
// budget of ONE translation call.
//
// 1 char ≈ 0.25 tokens and a faithful translation runs at roughly 1.3x the
// source token count in verbose target languages (German), so 2x the source
// tokens + 64 tokens of punctuation/template slack is a safe ceiling for every
// language the registry ships. It is a pure function so the budget contract is
// pinnable without a client.
func translationOutputBudget(sourceLen int) int {
	if sourceLen < 0 {
		sourceLen = 0
	}
	sourceTokens := (sourceLen + 3) / 4
	budget := sourceTokens*2 + 64
	if budget < minTranslationPredictTokens {
		budget = minTranslationPredictTokens
	}
	if budget > maxTranslationPredictTokens {
		budget = maxTranslationPredictTokens
	}
	return budget
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

	// Output budget of this call: bounded by the source length so the model
	// cannot ramble into an essay instead of a faithful translation.
	sourceLen := len([]rune(text))
	predictLimit := translationOutputBudget(sourceLen)

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

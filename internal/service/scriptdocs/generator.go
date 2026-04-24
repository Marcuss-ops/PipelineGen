package scriptdocs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// generateScriptForLang generates script text in a specific language via Ollama.
func (s *ScriptDocService) generateScriptForLang(ctx context.Context, topic string, duration int, langName, model string) (string, error) {
	genCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	prompt := s.buildPrompt(topic, duration, langName)

	result, err := s.generator.GetClient().GenerateWithOptions(genCtx, model, prompt, map[string]interface{}{"num_predict": 4096, "temperature": 0.7})
	if err != nil {
		return "", err
	}

	text := cleanPreamble(result)
	return text, nil
}

// generateScriptForLangWithRetry generates script text with exponential backoff retry.
func (s *ScriptDocService) generateScriptForLangWithRetry(ctx context.Context, topic string, duration int, langName, model string, maxRetries int) (string, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		text, err := s.generateScriptForLang(ctx, topic, duration, langName, model)
		if err == nil {
			return text, nil
		}

		lastErr = err
		if attempt < maxRetries-1 {
			// Exponential backoff: 1s, 2s, 4s, ...
			wait := time.Duration(1<<uint(attempt)) * time.Second
			logger.Warn("Script generation failed, retrying",
				zap.String("lang", langName),
				zap.Int("attempt", attempt+1),
				zap.Duration("wait", wait),
				zap.Error(err),
			)
			select {
			case <-time.After(wait):
				// Continue to next attempt
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	return "", fmt.Errorf("after %d attempts: %w", maxRetries, lastErr)
}

// GenerateScriptText generates a single script draft using the scriptdocs prompt
// and retry logic, without building docs or associations.
func (s *ScriptDocService) GenerateScriptText(ctx context.Context, topic string, duration int, language, template, model string) (string, error) {
	if strings.TrimSpace(language) == "" {
		language = "english"
	}

	langInfo, ok := LanguageInfo[language]
	promptLang := language
	if ok && strings.TrimSpace(langInfo.PromptLang) != "" {
		promptLang = langInfo.PromptLang
	}

	svc := *s
	if strings.TrimSpace(template) != "" {
		svc.currentTemplate = template
	}

	return svc.generateScriptForLangWithRetry(ctx, topic, duration, promptLang, model, 3)
}

// buildPrompt creates a customized prompt based on template type.
func (s *ScriptDocService) buildPrompt(topic string, duration int, langName string) string {
	wordCount := duration * 3 // ~3 words per second

	baseInstructions := ""
	switch s.getTemplate() {
	case TemplateStorytelling:
		baseInstructions = fmt.Sprintf("Genera testo NARRATIVO su %s (~%d parole) in lingua %s. Usa un arco narrativo con hook iniziale, conflitto e risoluzione. INIZIA con una domanda o fatto sorprendente.", topic, wordCount, langName)
	case TemplateTop10:
		baseInstructions = fmt.Sprintf("Genera testo TOP 10 LISTA su %s (~%d parole) in lingua %s. Struttura come 'I 10 fatti più importanti'. INIZIA direttamente con il numero 10.", topic, wordCount, langName)
	case TemplateBiography:
		baseInstructions = fmt.Sprintf("Genera testo BIOGRAFICO su %s (~%d parole) in lingua %s. Copri vita, carriera e impatto. INIZIA con la nascita o primo successo.", topic, wordCount, langName)
	default: // documentary
		baseInstructions = fmt.Sprintf("Genera testo COMPLETO su %s (~%d parole) in lingua %s.", topic, wordCount, langName)
	}

	return fmt.Sprintf(`%s

IMPORTANTE: Scrivi SOLO il testo del documentario. NON scrivere introduzioni tipo "Ecco", "Sure", "Aquí", "Voici". Inizia direttamente con il nome o il fatto principale.`, baseInstructions)
}

// getTemplate returns the current template (for use in methods that need it).
func (s *ScriptDocService) getTemplate() string {
	if s.currentTemplate == "" {
		return TemplateDocumentary
	}
	return s.currentTemplate
}

// preamblePrefixes matches common AI preamble patterns.
var preamblePrefixes = []string{
	"ecco", "sure", "here", "certamente", "certo", "certainly",
	"of course", "absolutely", "definitely", "ok", "okay", "bene", "perfetto",
}

// cleanPreamble strips AI preamble phrases from the beginning of generated text.
func cleanPreamble(text string) string {
	// Iteratively remove preamble phrases
	remaining := text
	for i := 0; i < 3; i++ { // Max 3 iterations to handle chained preambles
		for _, prefix := range preamblePrefixes {
			lower := strings.ToLower(strings.TrimSpace(remaining))
			if strings.HasPrefix(lower, prefix) {
				// Find the end of the preamble (colon, period, or newline)
				idx := strings.IndexAny(remaining[len(prefix):], ":.\n")
				if idx != -1 {
					remaining = strings.TrimSpace(remaining[len(prefix)+idx+1:])
				}
				break
			}
		}
	}
	return remaining
}

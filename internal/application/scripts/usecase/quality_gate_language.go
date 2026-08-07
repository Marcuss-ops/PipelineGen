// Package usecase — quality_gate_language.go
//
// Language detection metrics and the requested-vs-detected rule of the
// editorial quality gate.
package usecase

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
)

// languageMatchChecker fails when the detected language differs from the
// requested one.
type languageMatchChecker struct{}

func (languageMatchChecker) Name() string { return "language_match" }

func (languageMatchChecker) Check(in qualityGateInput) []string {
	requestedLang := strings.ToLower(strings.TrimSpace(in.plan.Language))
	if in.q.LanguageDetected != "" && requestedLang != "" && in.q.LanguageDetected != requestedLang {
		return []string{
			"detected language " + in.q.LanguageDetected + " does not match requested language " + requestedLang,
		}
	}
	return nil
}

// detectLanguage returns the ISO-639-1 language code with the highest
// overlap against the configured registry profiles. When no profile
// signals match, it returns an empty string.
func detectLanguage(text string) string {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return ""
	}

	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return ""
	}
	var scores []struct {
		code  string
		score float64
	}
	for _, code := range registry.Languages() {
		if code == "fallback" {
			continue
		}
		scores = append(scores, struct {
			code  string
			score float64
		}{code, languageMatchScore(tokens, registry.StopWords(code))})
	}

	maxScore := 0.0
	maxCode := ""
	for _, s := range scores {
		if s.score > maxScore {
			maxScore = s.score
			maxCode = s.code
		}
	}

	return maxCode
}

// languageMatchScore returns the ratio of stop words from a language
// that appear in the token list.
func languageMatchScore(tokens []string, stopWords map[string]struct{}) float64 {
	if len(tokens) == 0 || len(stopWords) == 0 {
		return 0.0
	}
	seen := 0
	for _, t := range tokens {
		if _, ok := stopWords[t]; ok {
			seen++
		}
	}
	return float64(seen) / float64(len(tokens))
}

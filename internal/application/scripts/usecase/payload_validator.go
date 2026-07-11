// Package scripts — payload_validator.go provides config-aware
// validation of the incoming GenerationEnvelopeV2 for
// POST /api/script/generate.
//
// It complements the structural checks in
// internal/domain/script/generation_envelope.go with limits that
// require runtime configuration (source_text size, token budget,
// ratio to target words).
package usecase

import (
	"fmt"
	"strings"
	"unicode/utf8"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// PayloadValidator validates a GenerationEnvelopeV2 against
// configurable limits. It is constructed from ScriptsConfig and
// is safe for concurrent use.
type PayloadValidator struct {
	maxSourceTextChars              int
	maxSourceTextBytes              int
	maxSourceTextTokens             int
	maxSourceTextToTargetWordsRatio float64
}

// NewPayloadValidator builds a validator from the supplied config.
// The config is applied with defaults via WithDefaults.
func NewPayloadValidator(cfg config.ScriptsConfig) *PayloadValidator {
	cfg = cfg.WithDefaults()
	return &PayloadValidator{
		maxSourceTextChars:              cfg.MaxSourceTextChars,
		maxSourceTextBytes:              cfg.MaxSourceTextBytes,
		maxSourceTextTokens:             cfg.MaxSourceTextTokens,
		maxSourceTextToTargetWordsRatio: cfg.MaxSourceTextToTargetWordsRatio,
	}
}

// NewDefaultPayloadValidator returns a validator with sensible
// production defaults. Useful for tests and for wiring paths that
// do not yet read ScriptsConfig.
func NewDefaultPayloadValidator() *PayloadValidator {
	return NewPayloadValidator(config.ScriptsConfig{})
}

// ValidateEnvelope runs payload-level validation on the envelope.
// It first delegates to the structural validator and then applies
// config-aware limits. Returns nil when the envelope is valid.
func (v *PayloadValidator) ValidateEnvelope(env *scriptpkg.GenerationEnvelopeV2) error {
	if env == nil {
		return &scriptpkg.PayloadValidationError{
			Code:    "INVALID_PAYLOAD",
			Message: "envelope is nil",
		}
	}

	if err := env.Validate(); err != nil {
		return err
	}

	for i, item := range env.Items {
		ref := item.ID
		if ref == "" {
			ref = fmt.Sprintf("item %d", i)
		}
		if err := v.validateItem(item, ref); err != nil {
			return err
		}
	}

	return nil
}

func (v *PayloadValidator) validateItem(item scriptpkg.GenerationItemV2, ref string) error {
	if item.ScriptParams.TargetWords <= 0 {
		return &scriptpkg.PayloadValidationError{
			Code:      "INVALID_TARGET_WORDS",
			Message:   "target_words must be > 0",
			Stage:     "request.validation",
			Retryable: false,
			Extra: map[string]any{
				"actual_target_words": item.ScriptParams.TargetWords,
			},
		}
	}

	if err := v.validateSourceText(item, ref); err != nil {
		return err
	}
	return nil
}

func (v *PayloadValidator) validateSourceText(item scriptpkg.GenerationItemV2, ref string) error {
	sourceText := strings.TrimSpace(item.Source.SourceText)
	if sourceText == "" {
		return nil
	}

	chars := utf8.RuneCountInString(sourceText)
	if v.maxSourceTextChars > 0 && chars > v.maxSourceTextChars {
		return &scriptpkg.PayloadValidationError{
			Code:      "SOURCE_TEXT_TOO_LARGE",
			Message:   "source_text exceeds maximum character limit",
			Stage:     "request.validation",
			Retryable: false,
			Extra: map[string]any{
				"actual_chars": chars,
				"max_chars":    v.maxSourceTextChars,
			},
		}
	}

	bytes := len(sourceText)
	if v.maxSourceTextBytes > 0 && bytes > v.maxSourceTextBytes {
		return &scriptpkg.PayloadValidationError{
			Code:      "SOURCE_TEXT_TOO_LARGE",
			Message:   "source_text exceeds maximum byte limit",
			Stage:     "request.validation",
			Retryable: false,
			Extra: map[string]any{
				"actual_bytes": bytes,
				"max_bytes":    v.maxSourceTextBytes,
			},
		}
	}

	tokens := estimateTokens(sourceText)
	if v.maxSourceTextTokens > 0 && tokens > v.maxSourceTextTokens {
		return &scriptpkg.PayloadValidationError{
			Code:      "SOURCE_TEXT_TOO_LARGE",
			Message:   "source_text exceeds maximum estimated token limit",
			Stage:     "request.validation",
			Retryable: false,
			Extra: map[string]any{
				"actual_tokens": tokens,
				"max_tokens":    v.maxSourceTextTokens,
			},
		}
	}

	words := countWords(sourceText)
	targetWords := item.ScriptParams.TargetWords
	if v.maxSourceTextToTargetWordsRatio > 0 && targetWords > 0 && float64(words) > v.maxSourceTextToTargetWordsRatio*float64(targetWords) {
		return &scriptpkg.PayloadValidationError{
			Code:      "SOURCE_TEXT_EXCEEDS_TARGET_RATIO",
			Message:   "source_text word count exceeds configured ratio to target_words",
			Stage:     "request.validation",
			Retryable: false,
			Extra: map[string]any{
				"source_words": words,
				"target_words": targetWords,
				"max_ratio":    v.maxSourceTextToTargetWordsRatio,
				"actual_ratio": float64(words) / float64(targetWords),
			},
		}
	}

	return nil
}

// estimateTokens returns a rough token estimate for the supplied
// text. The heuristic is ~4 characters per token for Latin scripts
// and ~1.5 characters per token for CJK scripts. This is intentionally
// cheap and dependency-free.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	var runes int
	var cjk int
	for _, r := range s {
		runes++
		if isCJK(r) {
			cjk++
		}
	}
	latin := runes - cjk
	// Avoid division by zero; the formula below is safe because
	// runes > 0 when s != "".
	tokens := int(float64(latin)/4.0 + float64(cjk)/1.5)
	if tokens < 1 {
		return 1
	}
	return tokens
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xAC00 && r <= 0xD7AF) ||
		(r >= 0x3040 && r <= 0x309F) ||
		(r >= 0x30A0 && r <= 0x30FF)
}

func countWords(s string) int {
	fields := strings.Fields(s)
	return len(fields)
}

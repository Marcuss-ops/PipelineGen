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
	// maxSegmentsCap caps the number of ScriptSegment entries
	// accepted on a single item. PR-CS-1 / FASE 6 (DoD #8).
	maxSegmentsCap int
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
		maxSegmentsCap:                  cfg.MaxSegmentsCap,
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
	// PR-CS-1 / FASE 6 (DoD #8): structural ScriptSegment shape —
	// run BEFORE config-aware checks so the handler path catches
	// mutex/empty/topic violations as HTTP 400 INVALID_PAYLOAD
	// without falling through into source_text ratio limits. The
	// validation order is intentional: structural > semantic >
	// config-aware. Delegates to validateScriptSegmentShape
	// (godlike/06 SSoT single canonical owner in
	// generation_validator.go).
	if details := validateScriptSegmentShape(item.ScriptParams, ref); len(details) > 0 {
		return &scriptpkg.PlanInvalidError{
			ItemID:  item.ID,
			Details: details,
		}
	}

	// PR-CS-1 / FASE 6 (DoD #8): target_words <= 0 is allowed when
	// the caller supplied ≥1 ScriptSegment (each per-block carries
	// its own TargetWords). Existing logic preserved verbatim — same
	// Code (INVALID_TARGET_WORDS), same Message, same Extra — only
	// the conjunction `&& len(Segments) == 0` is added.
	if item.ScriptParams.TargetWords <= 0 && len(item.ScriptParams.Segments) == 0 {
		return &scriptpkg.PayloadValidationError{
			Code:      "INVALID_TARGET_WORDS",
			Message:   "target_words must be > 0",
			Stage:     "request.validation",
			Retryable: false,
			Extra: scriptpkg.ValidationExtras{
				ActualTargetWords: item.ScriptParams.TargetWords,
			},
		}
	}

	if err := v.validateSegmentsCap(item); err != nil {
		return err
	}

	if err := validateProvidedVideoMetadata(item); err != nil {
		return err
	}

	if err := v.validateSourceText(item, ref); err != nil {
		return err
	}
	return nil
}

func validateProvidedVideoMetadata(item scriptpkg.GenerationItemV2) error {
	metadata := item.VideoMetadata
	if metadata == nil {
		return nil
	}

	if !metadata.HasContent() {
		return &scriptpkg.PayloadValidationError{
			Code:      "EMPTY_VIDEO_METADATA",
			Message:   "video_metadata must contain title, description, or tags",
			Stage:     "request.validation",
			Retryable: false,
		}
	}

	return nil
}

// validateSegmentsCap rejects oversized Segments payloads per the
// operator cap MaxSegmentsCap (config-aware). PR-CS-1 / FASE 6
// (DoD #8). Wire: HTTP 400 with Code="TOO_MANY_SEGMENTS" when
// len(Segments) > cap. A zero cap disables the gate (used by
// legacy wiring paths); WithDefaults sets it to 50 in production.
func (v *PayloadValidator) validateSegmentsCap(item scriptpkg.GenerationItemV2) error {
	sp := item.ScriptParams
	if v.maxSegmentsCap > 0 && len(sp.Segments) > v.maxSegmentsCap {
		return &scriptpkg.PayloadValidationError{
			Code:    "TOO_MANY_SEGMENTS",
			Message: fmt.Sprintf("script_params.segments has too many entries (max %d)", v.maxSegmentsCap),
			Stage:   "request.validation",
			Extra: scriptpkg.ValidationExtras{
				ActualSegments: len(sp.Segments),
				MaxSegmentsCap: v.maxSegmentsCap,
			},
		}
	}
	return nil
}

// sourceTextMetrics holds the measured dimensions of a source_text.
// It is intentionally limited to counts; the raw text is never stored
// here so that metrics helpers cannot accidentally log the full text.
type sourceTextMetrics struct {
	chars  int
	bytes  int
	tokens int
	words  int
}

// measureSourceText computes byte, character, estimated token and word
// counts for the supplied text. The original text is not retained.
func measureSourceText(text string) sourceTextMetrics {
	return sourceTextMetrics{
		chars:  utf8.RuneCountInString(text),
		bytes:  len(text),
		tokens: estimateTokens(text),
		words:  countWords(text),
	}
}

func (v *PayloadValidator) validateSourceText(item scriptpkg.GenerationItemV2, ref string) error {
	sourceText := strings.TrimSpace(item.Source.SourceText)
	if sourceText == "" {
		return nil
	}

	m := measureSourceText(sourceText)

	// Build a single SOURCE_TEXT_TOO_LARGE error that surfaces every
	// exceeded limit together with the actual and maximum values. The
	// raw source text is intentionally omitted from the error payload
	// and from any log fields to avoid leaking caller data.
	if exceeded := v.exceededSourceTextLimits(m); len(exceeded) > 0 {
		extra := scriptpkg.ValidationExtras{
			ActualChars:  m.chars,
			ActualBytes:  m.bytes,
			ActualTokens: m.tokens,
		}
		if v.maxSourceTextChars > 0 {
			extra.MaxChars = v.maxSourceTextChars
		}
		if v.maxSourceTextBytes > 0 {
			extra.MaxBytes = v.maxSourceTextBytes
		}
		if v.maxSourceTextTokens > 0 {
			extra.MaxTokens = v.maxSourceTextTokens
		}
		extra.Limits = exceeded
		return &scriptpkg.PayloadValidationError{
			Code:      "SOURCE_TEXT_TOO_LARGE",
			Message:   "source_text exceeds configured limits (chars/bytes/tokens)",
			Stage:     "request.validation",
			Retryable: false,
			Extra:     extra,
		}
	}

	words := m.words
	targetWords := item.ScriptParams.TargetWords
	if v.maxSourceTextToTargetWordsRatio > 0 && targetWords > 0 && float64(words) > v.maxSourceTextToTargetWordsRatio*float64(targetWords) {
		return &scriptpkg.PayloadValidationError{
			Code:      "SOURCE_TEXT_EXCEEDS_TARGET_RATIO",
			Message:   "source_text word count exceeds configured ratio to target_words",
			Stage:     "request.validation",
			Retryable: false,
			Extra: scriptpkg.ValidationExtras{
				SourceWords: words,
				TargetWords: targetWords,
				MaxRatio:    v.maxSourceTextToTargetWordsRatio,
				ActualRatio: float64(words) / float64(targetWords),
			},
		}
	}

	return nil
}

func (v *PayloadValidator) exceededSourceTextLimits(m sourceTextMetrics) []string {
	var exceeded []string
	if v.maxSourceTextChars > 0 && m.chars > v.maxSourceTextChars {
		exceeded = append(exceeded, "chars")
	}
	if v.maxSourceTextBytes > 0 && m.bytes > v.maxSourceTextBytes {
		exceeded = append(exceeded, "bytes")
	}
	if v.maxSourceTextTokens > 0 && m.tokens > v.maxSourceTextTokens {
		exceeded = append(exceeded, "tokens")
	}
	return exceeded
}

// estimateTokens returns a rough token estimate for the supplied
// text. The heuristic is ~4 characters per token for Latin scripts
// and ~1.5 characters per token for CJK scripts. This is intentionally
// cheap and dependency-free; the value reported in
// SOURCE_TEXT_TOO_LARGE errors is an estimate, not a real tokenizer
// count.
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

// Package scripts — source_text_log.go provides a canonical redaction
// helper for source_text so that logs never contain the full raw text.
//
// The helper surfaces only:
//   - source_text_hash (SHA-256 hex)
//   - source_text_chars (rune count)
//   - source_text_bytes (byte length)
//   - source_text_tokens (rough token estimate)
//   - source_text_preview (first N runes, optional/disabled)
//
// This is the single place where source_text is prepared for log
// fields; any code that wants to log source context must use this
// helper instead of logging the raw string.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
)

// SourceTextLogFields returns a map suitable for zap.Any/ zap.Any
// logging that describes source_text without ever including the full
// text. cfg controls whether the preview is included and how long it
// is. The returned map is always non-nil.
//
// Preview semantics: a preview is emitted only when the source text is
// strictly longer than the configured preview budget. This guarantees
// the logged preview can never be the full raw text, preventing
// accidental leakage of short inputs. When the text fits within the
// budget the preview field is omitted entirely.
func SourceTextLogFields(text string, cfg adapters.NormalizationConfig) map[string]any {
	fields := map[string]any{
		"source_text_hash":   hashSourceTextForLog(text),
		"source_text_chars":  utf8.RuneCountInString(text),
		"source_text_bytes":  len(text),
		"source_text_tokens": estimateTokens(text),
	}

	// Only emit a preview when the source text is strictly longer than the
	// preview budget. This guarantees the logged preview can never be the
	// full raw text, preventing accidental leakage of short inputs.
	if cfg.LogSourceTextPreview && cfg.SourceTextPreviewChars > 0 && utf8.RuneCountInString(text) > cfg.SourceTextPreviewChars {
		preview := previewSourceText(text, cfg.SourceTextPreviewChars)
		if preview != "" {
			fields["source_text_preview"] = preview
		}
	}

	return fields
}

// hashSourceTextForLog returns a SHA-256 hex digest of the raw text.
func hashSourceTextForLog(text string) string {
	h := digest.SHA256Bytes([]byte(text))
	return h
}

// previewSourceText returns the first maxRunes runes of text. It
// never splits a multi-byte rune and returns an empty string when
// text is empty.
func previewSourceText(text string, maxRunes int) string {
	if maxRunes <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

// Package voiceover — validation.go (PR-VOICEOVER-COMMAND-EXTRACT, June 2026).
//
// (*GenerateVoiceoversCommand).Validate runs the canonical validation
// gate ONCE at the use case boundary. Replaces per-call
// Destination.Validate that the legacy BatchRequest did at the entry
// point so every port invocation after Validate succeeds has a
// known-safe input envelope.
//
// Validation contract:
//
//   - Text non-empty (whitespace-stripped).
//   - Languages non-empty (one or more BCP-47 codes).
//   - Languages: each code is non-empty + alphanumeric (with `-`
//     allowed for BCP-47 subtags); strict BCP-47 grammar (region/
//     script/variant subtags) is NOT enforced — the audioasset
//     bridge accepts whatever Language string is passed; the gate is
//     the "valid ASCII identifier" so callers fail fast at the
//     request boundary.
//   - Destination (if non-nil): subfolder_name passes pathutil
//     sanitiser (delegated via DestinationRequest.Validate — PR-VO-A4).
//   - Parallelism >= 0 (< 0 -> error; the use case clamps to
//     min(requested, MaxParallelism, len(Languages)) at execute time).
package voiceover

import (
	"fmt"
	"strings"
)

// Validate runs the canonical validation gate ONCE at the use case
// boundary. Returns nil when every slot is safe to consume downstream.
// The use case MUST call cmd.Validate() BEFORE any port invocation
// (mirrors the path-traversal-rejection-before-field-access pattern
// pinned by TestGenerateBatch_RejectsPathTraversalPayload).
//
// Step 5 (P0.3 items-model recovery, June 2026): the P0.2 shared-text
// invariant is REMOVED. Each item is validated independently — mixed
// texts, duplicate languages with different voices, and per-item
// filenames are all first-class. The pre-step "all items must share
// the same text" rule is gone: every item's text is independently
// required; every item's language is independently required.
func (c *GenerateVoiceoversCommand) Validate() error {
	if c == nil {
		return fmt.Errorf("nil GenerateVoiceoversCommand")
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("items: must contain at least one item")
	}
	for i, it := range c.Items {
		// VoiceoverItem is a value-type struct (not a pointer), so an
		// item slot can never be nil — only a zero-value struct, which
		// the text/language checks below already reject (empty Text and
		// invalid Language are surfaced with a clearer error).
		if strings.TrimSpace(it.Text) == "" {
			return fmt.Errorf("items[%d].text: must be non-empty", i)
		}
		// PR-VO-TYPED-PRIMITIVES (July 2026): route through the
		// canonical ParseLanguage gate (typed sentinel on failure).
		// The underlying gate logic is identical to LanguageCodeValid
		// (validation.go below) — ParseLanguage just wraps the
		// typed-envelope conversion.
		if _, err := ParseLanguage(string(it.Language)); err != nil {
			return fmt.Errorf("items[%d].language: %w (only alphanumeric + hyphens allowed)", i, err)
		}
	}
	if c.Destination != nil {
		if vErr := c.Destination.Validate(); vErr != nil {
			return fmt.Errorf("destination: %w", vErr)
		}
	}
	if c.Parallelism < 0 {
		return fmt.Errorf("parallelism: must be >= 0 (clamped to 0 sequentially at use-case boundary)")
	}
	if c.Timing != nil {
		// Normalize first so an empty policy (all-zero slots) resolves to
		// the canonical defaults instead of failing; caller-explicit
		// invalid values still surface.
		if vErr := c.Timing.Normalized().Validate(); vErr != nil {
			return fmt.Errorf("voiceover_timing: %w", vErr)
		}
	}
	return nil
}

// LanguageCodeValid enforces non-empty BCP-47-style codes (alphanumeric
// + hyphen for subtags). Doesn't enforce strict BCP-47 grammar (region/
// script/variant subtags) — the gate is "valid ASCII identifier" so
// callers fail fast at the request boundary.
//
// Whitespace-stripped before the check so a caller sending " it-IT "
// passes (the audioasset.AudioInput.Language field strips spaces
// internally too; this gate is the early rejection point).
//
// PR-VO-TYPED-PRIMITIVES (July 2026): the canonical gate logic now
// lives in language.go::isLanguageCodeValid (package-private, shared
// with ParseLanguage). This function is preserved as a thin wrapper
// for back-compat with the existing test surface
// (validation_test.go pins LanguageCodeValid directly).
func LanguageCodeValid(code string) bool {
	return isLanguageCodeValid(strings.TrimSpace(code))
}

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
func (c *GenerateVoiceoversCommand) Validate() error {
	if c == nil {
		return fmt.Errorf("nil GenerateVoiceoversCommand")
	}
	if strings.TrimSpace(c.Text) == "" {
		return fmt.Errorf("text: must be non-empty")
	}
	if len(c.Languages) == 0 {
		return fmt.Errorf("languages: must contain one or more BCP-47 codes")
	}
	for _, lang := range c.Languages {
		if !LanguageCodeValid(lang) {
			return fmt.Errorf("languages: invalid code %q (only alphanumeric + hyphens allowed)", lang)
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
func LanguageCodeValid(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	for _, r := range code {
		if r == '-' {
			continue
		}
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

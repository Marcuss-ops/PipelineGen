// Package jsonextract — fresh_parser.go owns the canonical fresh-mode
// plain-prose gate. PR-5 of the LLM-PLAIN-TEXT-CONTRACT wave made
// raw narrative prose the canonical primary input shape for fresh
// generation paths. This file is the SOLE canonical owner of the
// untagged-prose → ModelScriptOutputV1 composition for fresh mode.
//
// godlike/06 SSOT (one canonical owner per fact): the fresh-mode
// route MUST route through this file. Legacy compatibility helpers
// (legacy_converter.go) own a different concern (cache-replay of
// pre-V1 legacy arrays) and are NOT the fresh-mode owner.
//
// godlike/07 NO-FAKE-AVAILABILITY: ParsePlainTextFresh rejects
// legacy-JSON-shaped input (object or array) with
// ErrModelOutputMalformed so a future LLM silently falling back to
// the deprecated V1 contract is observable (NOT silently absorbed
// into a prose scene). Plain-prose input (no leading `{` or `[`)
// is ALWAYS wrapped.
package jsonextract

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ParsePlainTextFresh is the EXPORTED canonical entry point for
// fresh-mode plain-prose LLM output (LLM-PLAIN-TEXT-CONTRACT wave
// PR-5). It wraps the binary untagged-prose → ModelScriptOutputV1
// envelope composition in a typed-sentinel envelope so callers can
// probe failures via errors.Is(err, scriptpkg.ErrModelOutputMalformed).
//
// godlike/06 SSOT: this function is the SOLE external entry point
// for fresh-mode prose. Any future caller wanting to wrap raw LLM
// output for fresh mode MUST route through ParsePlainTextFresh —
// no direct usage of legacy compatibility helpers from outside the
// package, and no parallel implementation in any sibling file.
//
// godlike/07 NO-FAKE-AVAILABILITY: rejects legacy-JSON-shaped input
// (object or array, including JSON-string-wrapped) with
// ErrModelOutputMalformed so a future LLM silently falling back to
// the deprecated V1 contract is observable (NOT silently absorbed
// into a prose scene). Plain-prose input (no leading `{` or `[`)
// is ALWAYS wrapped.
//
// godlike/07 typed-error contract: ErrModelOutputMalformed wrapped
// via fmt.Errorf("%w: ...") so errors.Is and errors.As both work
// per the Go 1.20+ dual-%w idiom.
func ParsePlainTextFresh(raw []byte) (*scriptpkg.ModelScriptOutputV1, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty output", scriptpkg.ErrModelOutputMalformed)
	}

	// ── Legacy-JSON guard: check BEFORE cleanFallbackText ──────
	//
	// cleanFallbackText extracts prose from JSON envelopes (e.g.
	// {"schema_version":1,"text":"hello"} → "hello"), so checking
	// looksLikeJSON AFTER stripping would silently accept every
	// legacy-V1 payload as plain prose. The guard below runs on the
	// raw input to catch:
	//  1. Bare JSON objects ({...}) and arrays ([...]).
	//  2. JSON-string-wrapped objects ("{...}") — a known LLM
	//     output pattern where the model double-wraps its JSON.
	rawStr := strings.TrimSpace(string(raw))
	if isLegacyJSONShape(rawStr) {
		return nil, fmt.Errorf("%w: legacy JSON envelope detected on fresh plain-text path; the LLM is honouring the deprecated V1 contract — caller MUST either re-emit without JSON framing OR explicitly opt-in via ModeCompatibility for cache-replay",
			scriptpkg.ErrModelOutputMalformed)
	}

	trimmed := cleanFallbackText(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty output after JSON-envelope stripping", scriptpkg.ErrModelOutputMalformed)
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          trimmed,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  []scriptpkg.SpecScene{},
		},
	}, nil
}

// isLegacyJSONShape returns true when text is a JSON object, JSON
// array, or a JSON-quoted string whose content is a JSON object or
// array. It is load-bearing for ParsePlainTextFresh's godlike/07
// NO-FAKE-AVAILABILITY contract — it MUST fire BEFORE cleanFallbackText
// because cleanFallbackText extracts prose from inside JSON envelopes.
//
// Kept unexported (lowercase identifier) so external callers cannot
// diverge from the canonical fresh-mode contract. Called solely by
// ParsePlainTextFresh (this file's canonical gate); no other
// package member invokes it directly.
func isLegacyJSONShape(text string) bool {
	if looksLikeJSON(text) {
		return true
	}
	if unquoted, ok := tryUnquoteJSONString(text); ok {
		return looksLikeJSON(strings.TrimSpace(unquoted))
	}
	return false
}

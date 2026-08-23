// Package jsonextract — scanner.go provides a parametrized JSON-output
// scanner that accepts raw LLM output bytes and decodes them into a
// typed ModelScriptOutputV1. It replaces the two pre-P0.8 decoders
// (model_output_decoder.go + compat/legacy_model_output_decoder.go)
// and the now-removed ModeCompatibility (legacy array + plain-text
// fallback) path.
//
//   ModeFreshPlainText — the sole canonical mode (zero value).
//                        Try V1 JSON envelope first; on JSON-shaped
//                        input, surface ErrModelOutputMalformed
//                        (do not silently wrap as prose); on plain
//                        prose, delegate to ParsePlainTextFresh which
//                        wraps the prose and is the canonical PRIMARY
//                        path for fresh-mode LLM contracts.
//   ModeStrict         — DEPRECATED same-value alias for ModeFreshPlainText.
//                        Retained for backward-compat with existing
//                        callers (tests, engine_generate.go). New code
//                        MUST use ModeFreshPlainText.
//
// The scanner is self-contained: JSON extraction is handled by extractor.go;

package jsonextract

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Mode ────────────────────────────────────────────────────────────

// Mode controls the fallback behaviour of the Scanner.
type Mode int

const (
	// ModeFreshPlainText is the CANONICAL name for the fresh-mode
	// behaviour. It owns the iota=0 slot (the zero-value default);
	// callers that don't construct a Scanner explicitly land here.
	//
	// Behaviour:
	//  1. Try to extract a V1 JSON envelope; if successful and valid
	//     on schema_version, return it.
	//  2. If the input LOOKS like a JSON envelope (object, array, or
	//     JSON-string-wrapped object/array) but decodeV1 failed, the
	//     LLM is honouring a structured contract — surface
	//     ErrModelOutputMalformed (godlike/07 NO-FAKE-AVAILABILITY).
	//     Do NOT silently fall back to prose wrapping.
	//  3. Otherwise (raw prose), delegate to ParsePlainTextFresh
	//     (canonical gate in fresh_parser.go) which wraps the input
	//     into a ModelScriptOutputV1 with empty scenes.
	ModeFreshPlainText Mode = iota
)

// ModeStrict is a DEPRECATED same-value alias for ModeFreshPlainText.
// It is declared as its own top-level const (OUTSIDE the iota-driven
// block above) to avoid fragmenting the implicit-expression chain.
// Retained so existing callers (tests, engine_generate.go) continue
// to compile unchanged. New code MUST use ModeFreshPlainText.
const ModeStrict = ModeFreshPlainText

// String returns a human-readable mode name for log/diagnostics.
func (m Mode) String() string {
	switch m {
	case ModeFreshPlainText:
		return "fresh_plain_text"
	default:
		return "unknown"
	}
}

// ── Scanner ─────────────────────────────────────────────────────────

// Scanner decodes raw LLM output bytes into a typed
// ModelScriptOutputV1. Its Mode controls whether legacy
// fallbacks are permitted.
//
// Zero value is ModeFreshPlainText (and therefore also ModeStrict
// via the deprecated-alias constant — they share the same numeric
// slot).
type Scanner struct {
	Mode Mode
}

// NewScanner constructs a Scanner with the given mode.
func NewScanner(mode Mode) *Scanner {
	return &Scanner{Mode: mode}
}

// Scan decodes raw LLM output bytes.
//
// Behaviour:
//  1. Try to extract a V1 JSON envelope; if successful and valid,
//     return it.
//  2. If the input LOOKS like a JSON envelope but decodeV1 failed,
//     surface ErrModelOutputMalformed (no silent fallback).
//  3. Otherwise (raw prose), delegate to ParsePlainTextFresh.
func (s *Scanner) Scan(raw []byte, _ string) (*scriptpkg.ModelScriptOutputV1, error) {
	if s == nil {
		s = &Scanner{}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty output", scriptpkg.ErrModelOutputMalformed)
	}

	jsonBytes, extractErr := extractJSON(raw)

	// ── ModeFreshPlainText (canonical; ModeStrict alias shares this slot) ──
	// ── V1 JSON fast-lane, plain-text wrap AS THE PRIMARY PATH (PR-5 flip) ─────
	//
	// PR-5 of the LLM-PLAIN-TEXT-CONTRACT wave inverts the original
	// "ModeStrict => JSON required, no fallbacks" logic. Fresh-mode
	// generation now expects raw narrative prose (see PR-1 prompt
	// flip + PR-2 OutputModePlainText const). The fresh path
	// therefore:
	//
	//  1. Try decodeV1 (canonical V1 JSON envelope) → success returns.
	//  2. On decodeV1 failure or no JSON, delegate to ParsePlainTextFresh
	//     which is the CANONICAL PRIMARY path for plain prose. The
	//     delegate itself enforces the typed-envelope contract:
	//     legacy-JSON-shaped input returns ErrModelOutputMalformed
	//     (NOT a silent-success wrap); un-tagged prose wraps cleanly.
	//
	// Pre-PR-5 behaviour was "no fallbacks" — a deprecated V1
	// contract violation would surface as ErrModelOutputMalformed
	// upstream. Post-PR-5 behaviour is "primary text path is
	// ParsePlainTextFresh (the LLM-PLAIN-TEXT contract)"; the JSON
	// path is now the OPTIONAL fast-lane, not the only legal input.
	//
	// godlike/06 SSOT: ParsePlainTextFresh (the canonical gate)
	// lives ONLY at fresh_parser.go. Scanner is a router, not a
	// decoder. godlike/07 NO-FAKE-AVAILABILITY: legacy-JSON detection
	// is owned by ParsePlainTextFresh (single typed-sentinel surface).
	// ModeStrict shares the numeric slot with ModeFreshPlainText via the
	// deprecated-alias constant, so a single path covers both names.
	//
	// LLM-PLAIN-TEXT contract: first try to extract a canonical V1 JSON
	// envelope. When extraction succeeds, the decoded/validated result
	// (or its error) is returned directly — no silent fallback.
	// When extraction fails, JSON-shaped input is a hard error;
	// non-JSON input falls through to the plain-prose primary path.
	if extractErr == nil {
		return decodeV1(jsonBytes)
	}
	if isLegacyJSONShape(string(raw)) {
		return nil, fmt.Errorf("%w: %v", scriptpkg.ErrModelOutputMalformed, extractErr)
	}
	// PRIMARY path: plain prose (fresh-mode LLM contract).
	return ParsePlainTextFresh(raw)
}

// decodeV1 unmarshals JSON bytes into ModelScriptOutputV1 and
// validates the result.
//
// If the outer specscene has zero scenes but the text field contains
// valid V1 JSON with non-empty scenes (double-wrapped output — a known
// LLM pattern with certain models), the inner scenes are promoted into
// the outer specscene. This preserves the structured scene data that
// postprocessors (images, voiceover) depend on.
func decodeV1(jsonBytes []byte) (*scriptpkg.ModelScriptOutputV1, error) {
	var output scriptpkg.ModelScriptOutputV1
	if err := json.Unmarshal(jsonBytes, &output); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %w", scriptpkg.ErrModelOutputMalformed, err)
	}
	normalizeOutput(&output)
	if err := output.Validate(); err != nil {
		return nil, err
	}

	return &output, nil
}

func normalizeOutput(output *scriptpkg.ModelScriptOutputV1) {
	if output == nil {
		return
	}

	output.Text = normalizeTextField(output.Text)
	for i := range output.SpecScene.Scenes {
		output.SpecScene.Scenes[i].Text = normalizeTextField(output.SpecScene.Scenes[i].Text)
	}

	// Double-wrapped JSON recovery: outer specscene is empty, but
	// the text field contains valid JSON with real scenes.
	if len(output.SpecScene.Scenes) == 0 && len(output.Text) > 0 {
		scanner := &Scanner{Mode: ModeFreshPlainText}
		if inner, err := scanner.Scan([]byte(output.Text), "double-wrap-recovery"); err == nil {
			if len(inner.SpecScene.Scenes) > 0 {
				output.SpecScene = inner.SpecScene
				for i := range output.SpecScene.Scenes {
					output.SpecScene.Scenes[i].Text = normalizeTextField(output.SpecScene.Scenes[i].Text)
				}
				output.Text = normalizeTextField(inner.Text)
			}
		}
	}
}

func normalizeTextField(text string) string {
	current := strings.TrimSpace(text)
	for i := 0; i < 2; i++ {
		if current == "" {
			return ""
		}

		if unquoted, ok := tryUnquoteJSONString(current); ok {
			current = strings.TrimSpace(unquoted)
			continue
		}

		if looksLikeJSON(current) {
			var inner scriptpkg.ModelScriptOutputV1
			if err := json.Unmarshal([]byte(current), &inner); err == nil && strings.TrimSpace(inner.Text) != "" {
				current = strings.TrimSpace(inner.Text)
				continue
			}
		}

		break
	}
	return current
}

func looksLikeJSON(text string) bool {
	text = strings.TrimSpace(text)
	return len(text) > 0 && (text[0] == '{' || text[0] == '[')
}

func tryUnquoteJSONString(text string) (string, bool) {
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return "", false
	}
	var unquoted string
	if err := json.Unmarshal([]byte(text), &unquoted); err != nil {
		return "", false
	}
	return unquoted, true
}

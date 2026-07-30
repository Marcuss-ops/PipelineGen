// Package jsonextract — scanner.go provides a parametrized JSON-output
// scanner that accepts raw LLM output bytes and decodes them into a
// typed ModelScriptOutputV1. It replaces the two pre-P0.8 decoders
// (model_output_decoder.go + compat/legacy_model_output_decoder.go)
// with a single Scanner whose Mode controls the fallback behaviour.
//
//   ModeFreshPlainText — canonical alias for ModeStrict (zero value).
//                        Try V1 JSON envelope first; on JSON-shaped
//                        input, surface ErrModelOutputMalformed
//                        (do not silently wrap as prose); on plain
//                        prose, delegate to ParsePlainTextFresh which
//                        wraps the prose and is the canonical PRIMARY
//                        path for fresh-mode LLM contracts.
//   ModeStrict         — DEPRECATED alias for ModeFreshPlainText.
//                        Kept as same-value constant so all existing
//                        callers (tests, engine) compile unchanged.
//                        New code MUST use ModeFreshPlainText.
//   ModeCompatibility  — V1 → legacy array (bump metric) → plain text
//                        wrapper (bump metric) → error. For
//                        cache-replay paths where pre-V1 rows may
//                        still be present.
//
// The scanner is self-contained: it registers its own Prometheus
// counters via promauto so no internal/infrastructure imports are
// needed. All JSON extraction and legacy-array conversion is handled
// by the sibling files in this package (fresh_parser.go owns the
// fresh-mode gate; legacy_converter.go owns the legacy-array
// conversion and compatibility fallback helpers).

package jsonextract

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Mode ────────────────────────────────────────────────────────────

// Mode controls the fallback behaviour of the Scanner.
type Mode int

const (
	// ModeFreshPlainText is the CANONICAL name for the fresh-mode
	// behaviour. It owns the iota=0 slot (the zero-value default);
	// callers that don't construct a Scanner explicitly land here.
	//
	// Behaviour (locks post-PR-5 of the LLM-PLAIN-TEXT-CONTRACT wave):
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

	// ModeCompatibility tries the canonical V1 decoder first, then
	// falls back to legacy-array conversion (bumping the
	// LegacyArrayFallbackTotal counter), then falls back to a
	// plain-text wrapper (bumping PlainTextFallbackTotal). All
	// three fail → typed error. This mode exists for cache-replay
	// paths where pre-V1 rows may still be present.
	ModeCompatibility
)

// ModeStrict is a DEPRECATED same-value alias for ModeFreshPlainText.
// It is declared as its own top-level const (OUTSIDE the
// iota-driven block above) so the implicit-expression chain inside
// that block does not fragment — bare identifiers after an explicit
// `B = A` assignment continue to reuse `A` instead of stepping
// through iota, which would silently collapse ModeCompatibility to
// the same numeric slot as ModeFreshPlainText.
//
// It is retained so existing callers (tests, engine_generate.go,
// retry paths) continue to compile unchanged. New code MUST use
// ModeFreshPlainText.
//
// godlike/07 contract-correction: the pre-rename documentation
// (gone after this commit) mis-described ModeStrict as "V1 JSON
// only; plain text and legacy arrays error." The actual runtime
// behaviour has been plain-prose-primary since PR-4/PR-5. The
// rename aligns the name with reality so the verifier-comment,
// docs, operator logs, and matrix tests no longer contradict
// each other.
const ModeStrict = ModeFreshPlainText

// String returns a human-readable mode name for log/diagnostics.
func (m Mode) String() string {
	switch m {
	case ModeFreshPlainText:
		// ModeStrict shares this numeric slot via the deprecated-
		// alias constant; the operator dashboard grep target is
		// "fresh_plain_text" so future callsites all converge on
		// the canonical name.
		return "fresh_plain_text"
	case ModeCompatibility:
		return "compatibility"
	default:
		return "unknown"
	}
}

// ── Metrics ─────────────────────────────────────────────────────────

var (
	// LegacyArrayFallbackTotal counts every time the compatibility
	// scanner falls back from V1 JSON to legacy-array conversion.
	// Replacement for the now-removed observability.LegacyArrayToOutputInvocationsTotal
	// counter formerly in metrics.go.
	LegacyArrayFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jsonextract_legacy_array_fallback_total",
		Help: "Monotonic counter for legacy-array fallback conversions inside jsonextract.Scanner (replaces observability.LegacyArrayToOutputInvocationsTotal). Source label: cache or fresh.",
	}, []string{"source"})

	// PlainTextFallbackTotal counts every time the compatibility
	// scanner falls back to plain-text wrapping (raw prose with no
	// JSON at all). In ModeStrict this path is never reached.
	PlainTextFallbackTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "jsonextract_plain_text_fallback_total",
		Help: "Monotonic counter for plain-text fallback conversions inside jsonextract.Scanner. Non-zero means the model emitted bare prose despite the V1 output instruction.",
	})
)

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

// Scan decodes raw LLM output bytes. The behaviour depends on Mode.
//
// ModeStrict:
//  1. Extract JSON (object or array) from raw bytes.
//  2. Unmarshal into ModelScriptOutputV1.
//  3. Validate.
//  4. Any failure → error wrapping script.ErrModelOutputMalformed.
//
// ModeCompatibility:
//  1. Extract JSON from raw bytes.
//     2a. Try unmarshal as V1 object → if OK and valid, return.
//     2b. Try unmarshal as legacy array → convert to V1, bump
//     LegacyArrayFallbackTotal{source}, return.
//     2c. Wrap raw bytes as plain-text V1, bump
//     PlainTextFallbackTotal, return.
//  3. All fail → error wrapping script.ErrModelOutputMalformed.
//
// The source label is passed to the Prometheus counters so
// operators can distinguish cache-replay from fresh-generation
// fallbacks. When empty it defaults to "unknown".
func (s *Scanner) Scan(raw []byte, source string) (*scriptpkg.ModelScriptOutputV1, error) {
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
	if s.Mode == ModeFreshPlainText {
		// ModeStrict shares this numeric slot via the deprecated-
		// alias constant declared at the top of this file, so
		// a single equality check covers both names.
		//
		// PR-5 LLM-PLAIN-TEXT contract: fresh mode first tries to
		// extract a canonical V1 JSON envelope. When extraction
		// succeeds, the decoded/validated result (or its error) is
		// returned directly — no silent fallback to plain prose.
		// When extraction fails, JSON-shaped input is an hard error;
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

	// ── ModeCompatibility: cascading fallbacks ───────────────────
	if source == "" {
		source = "unknown"
	}

	// 2a — canonical V1 JSON object.
	if extractErr == nil {
		if output, err := decodeV1(jsonBytes); err == nil {
			return output, nil
		}
	}

	// 2b — legacy array (pre-V1 cache rows).
	if legacy, err := convertLegacyArray(raw); err == nil {
		LegacyArrayFallbackTotal.WithLabelValues(source).Inc()
		return legacy, nil
	}

	// 2c — plain-text wrapper (model emitted prose).
	PlainTextFallbackTotal.Inc()
	return wrapPlainText(raw), nil
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
		scanner := &Scanner{Mode: ModeCompatibility}
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

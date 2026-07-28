// Package jsonextract — legacy_converter.go converts legacy array-shaped
// LLM output to the canonical ModelScriptOutputV1. This is the
// compatibility path for pre-V1 cache rows; it replaces
// compat/legacy_model_output_decoder.go::LegacyArrayToOutput.
//
// Legacy shape (observed in ollama responses before June 2026):
//
//	[
//	  {"index":0, "text":"...", "kind":"narration"},
//	  {"index":1, "text":"...", "kind":"clip", "clip_id":"clip-123"}
//	]

package jsonextract

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

var legacySceneKinds = map[string]scriptpkg.SceneKind{
	"narration": scriptpkg.SceneNarration,
	"narrator":  scriptpkg.SceneNarration,
	"voice":     scriptpkg.SceneNarration,
	"clip":      scriptpkg.SceneClip,
	"video":     scriptpkg.SceneClip,
	"footage":   scriptpkg.SceneClip,
	"image":     scriptpkg.SceneImage,
	"picture":   scriptpkg.SceneImage,
	"visual":    scriptpkg.SceneImage,
	"mixed":     scriptpkg.SceneMixed,
	"hybrid":    scriptpkg.SceneMixed,
}

// resolveCanonicalLegacySceneKind returns the canonical SceneKind for a
// legacy-V1 kind string. Unknown values fall back to SceneNarration.
func resolveCanonicalLegacySceneKind(kind string) scriptpkg.SceneKind {
	if resolved, ok := legacySceneKinds[kind]; ok {
		return resolved
	}
	return scriptpkg.SceneNarration
}

// legacyScene is the internal representation of a single scene element
// from the legacy JSON array output format.
type legacyScene struct {
	Index               int    `json:"index"`
	Text                string `json:"text"`
	Content             string `json:"content"` // alternate field name
	Title               string `json:"title"`
	Kind                string `json:"kind"`
	ClipID              string `json:"clip_id"`
	ClipTitle           string `json:"clip_title"`
	DriveLink           string `json:"drive_link"`
	ImageID             string `json:"image_id"`
	ImagePrompt         string `json:"image_prompt"`
	ImageURL            string `json:"image_url"`
	ImageStatus         string `json:"image_status"`
	VoiceoverStatus     string `json:"voiceover_status"`
	VoiceoverLink       string `json:"voiceover_link"`
	VoiceoverDurationMs int64  `json:"voiceover_duration_ms"`
}

// convertLegacyArray converts legacy array-shaped JSON bytes to a
// canonical ModelScriptOutputV1. Returns nil plus an error if the
// input is not a valid legacy array or is empty.
//
// This replaces compat.LegacyArrayToOutput from the now-removed
// compat/legacy_model_output_decoder.go.
func convertLegacyArray(raw []byte) (*scriptpkg.ModelScriptOutputV1, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty legacy array", scriptpkg.ErrModelOutputMalformed)
	}

	jsonBytes, err := extractJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", scriptpkg.ErrModelOutputMalformed, err)
	}

	// Only arrays are valid legacy input.
	var legacyScenes []legacyScene
	if err := json.Unmarshal(jsonBytes, &legacyScenes); err != nil {
		return nil, fmt.Errorf("%w: invalid legacy array JSON: %w",
			scriptpkg.ErrModelOutputMalformed, err)
	}
	if len(legacyScenes) == 0 {
		return nil, fmt.Errorf("%w: empty legacy scene array",
			scriptpkg.ErrModelOutputMalformed)
	}

	scenes := make([]scriptpkg.SpecScene, len(legacyScenes))
	var textParts []string

	for i, ls := range legacyScenes {
		sceneID := fmt.Sprintf("legacy-scene-%d", ls.Index)
		text := strings.TrimSpace(ls.Text)
		if text == "" && ls.Content != "" {
			text = strings.TrimSpace(ls.Content)
		}

		kind := scriptpkg.SceneKind(strings.TrimSpace(ls.Kind))
		if !kind.Valid() {
			kind = resolveCanonicalLegacySceneKind(strings.ToLower(ls.Kind))
		}

		scene := scriptpkg.SpecScene{
			ID:    sceneID,
			Index: ls.Index,
			Text:  text,
			Title: ls.Title,
			Kind:  kind,
		}

		if ls.ClipID != "" {
			scene.Bindings.Clip = &scriptpkg.ClipBinding{
				ClipID:    ls.ClipID,
				ClipTitle: ls.ClipTitle,
				DriveLink: ls.DriveLink,
			}
		}
		if ls.ImageURL != "" || ls.ImagePrompt != "" {
			scene.Bindings.Image = &scriptpkg.ImageBinding{
				ImageID: ls.ImageID,
				Prompt:  ls.ImagePrompt,
				URL:     ls.ImageURL,
				Status:  ls.ImageStatus,
			}
		}
		if ls.VoiceoverStatus != "" || ls.VoiceoverLink != "" {
			scene.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{
				Status:     ls.VoiceoverStatus,
				Link:       ls.VoiceoverLink,
				DurationMs: ls.VoiceoverDurationMs,
			}
		}

		scenes[i] = scene
		if text != "" {
			textParts = append(textParts, text)
		}
	}

	output := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          strings.Join(textParts, "\n\n"),
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}

	if err := output.Validate(); err != nil {
		return nil, fmt.Errorf("legacy conversion produced invalid output: %w", err)
	}
	return output, nil
}

// wrapPlainText wraps raw bytes as a synthetic ModelScriptOutputV1
// with the full text in the Text field and empty scenes. This is the
// canonical PLANE-PROSE wrapping primitive for the LLM-PLAIN-TEXT
// contract wave (PR-5 of PR-1..PR-6).
//
// PR-5 (the Flip): this function is the canonical PRIMARY entry
// path for fresh-mode plain-prose LLM output. It is still lowercase
// (unexported) so the canonical SOLE write seam for the untagged-prose
// → ModelScriptOutputV1 composition lives ONLY here per godlike/06
// SSOT one-canonical-owner-per-fact. Public callers MUST route
// through ParsePlainTextFresh (the exported gate that enforces the
// typed-sentinel contract on legacy-JSON input — see below).
//
// Pre-PR-5: this was the last-resort fallback for ModeCompatibility
// only. After PR-5: the canonical primary path for ModePlainTextFresh
// scanner routes (ModePlainTextFresh ships in a future PR; today
// it is the typed-enveloped sentinel-aware retry path ModeStrict
// falls into when JSON extraction fails).
//
// This replaces fallbackTextOutput from the now-removed
// model_output_decoder.go.
func wrapPlainText(raw []byte) *scriptpkg.ModelScriptOutputV1 {
	text := cleanFallbackText(string(raw))
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          text,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  []scriptpkg.SpecScene{},
		},
	}
}

// ParsePlainTextFresh is the EXPORTED canonical entry point for
// fresh-mode plain-prose LLM output (LLM-PLAIN-TEXT-CONTRACT wave
// PR-5). It wraps the binary untagged-prose → ModelScriptOutputV1
// envelope composition in a typed-sentinel envelope so callers can
// probe failures via errors.Is(err, scriptpkg.ErrModelOutputMalformed).
//
// godlike/06 SSOT (one canonical owner per fact): the
// untagged-prose → ModelScriptOutputV1 composition logic lives in the
// unexported wrapPlainText below; this function is the SOLE external
// entry point. Any future caller wanting to wrap raw LLM output
// for fresh mode MUST route through ParsePlainTextFresh — no
// direct usage of wrapPlainText from outside the package.
//
// godlike/07 NO-FAKE-AVAILABILITY: rejects legacy-JSON-shaped input
// (object or array) with ErrModelOutputMalformed so a future LLM
// silently falling back to the deprecated V1 contract is observable
// (NOT silently absorbed into a prose scene). Plain-prose input
// (no leading `{` or `[`) is ALWAYS wrapped.
//
// godlike/07 typed-error contract: ErrModelOutputMalformed wrapped
// via fmt.Errorf("%w: ...") so errors.Is and errors.As both work
// per the Go 1.20+ dual-%w idiom.
//
// godlike/07 minimum-blast-radius: zero new dependencies, zero new
// composition-root wiring, zero signature changes on existing
// callers (wrapPlainText is UNCHANGED; only scanner.go ModeStrict
// route calls ParsePlainTextFresh instead of returning a typed
// error directly on JSON-decode failure so a future body of
// legacy-JSON still surfaces ErrModelOutputMalformed upstream).
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
		return nil, fmt.Errorf("%w: legacy JSON envelope detected on fresh plain-text path; the LLM is honouring the deprecated V1 contract — caller MUST either re-emit without JSON framing OR explicitly opt-in via ModeLegacyJSONCache",
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
func isLegacyJSONShape(text string) bool {
	if looksLikeJSON(text) {
		return true
	}
	if unquoted, ok := tryUnquoteJSONString(text); ok {
		return looksLikeJSON(strings.TrimSpace(unquoted))
	}
	return false
}

// cleanFallbackText removes obvious JSON-envelope noise from a raw
// prose fallback before it reaches downstream splitters.
//
// The compatibility path lands here only after structured decoding
// failed, so this is intentionally conservative: it strips a leading
// JSON-looking envelope when there is trailing prose to preserve, and
// otherwise leaves the text alone.
func cleanFallbackText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	if unquoted, ok := tryUnquoteJSONString(text); ok {
		text = strings.TrimSpace(unquoted)
	}

	if len(text) == 0 {
		return ""
	}

	if text[0] == '{' || text[0] == '[' {
		open := text[0]
		close := closingDelim(open)
		if end := findMatchingDelim(text, open, close); end > 0 {
			head := strings.TrimSpace(text[:end+1])
			tail := strings.TrimSpace(text[end+1:])
			if tail != "" && isJsonEnvelopeNoise(head) {
				return tail
			}
			if isJsonEnvelopeNoise(head) {
				if extracted := extractFallbackEnvelopeText(head); extracted != "" {
					return extracted
				}
			}
		}
	}

	return text
}

func isJsonEnvelopeNoise(text string) bool {
	return strings.Contains(text, `"schema_version"`) ||
		strings.Contains(text, `"specscene"`) ||
		strings.Contains(text, `"text"`)
}

func extractFallbackEnvelopeText(text string) string {
	var output scriptpkg.ModelScriptOutputV1
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		return ""
	}
	return strings.TrimSpace(output.Text)
}

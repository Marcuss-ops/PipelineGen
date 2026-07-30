// Package jsonextract — legacy_converter.go owns the legacy-array
// converter and the ModeCompatibility-fallback helpers. The fresh-
// mode prose gate lives in fresh_parser.go; legacy_converter.go is
// strictly the compatibility-path owner.
//
// Two distinct concerns cohabit here historically:
//
//  1. Legacy-array conversion (convertLegacyArray). It transcodes
//     the pre-V1 array-shaped LLM output (observed in ollama
//     responses before June 2026) into the canonical V1 envelope.
//     This is the router path for ModeCompatibility and the
//     retry-after-ModeFreshPlainText-failure path for cache replay.
//
// Legacy shape (observed in ollama responses before June 2026):
//
//	[
//	  {"index":0, "text":"...", "kind":"narration"},
//	  {"index":1, "text":"...", "kind":"clip", "clip_id":"clip-123"}
//	]
//
//  2. Plain-text fallback primitive (wrapPlainText). For
//     ModeCompatibility only — it produces a synthetic
//     ModelScriptOutputV1 with the raw bytes as the Text field and
//     empty Scenes. NOT used by ModeFreshPlainText (which routes
//     through ParsePlainTextFresh in fresh_parser.go).
//
// godlike/06 SSOT: this file is the canonical owner of the
// compatibility path. Fresh-mode callers MUST route through
// fresh_parser.go even though cleanFallbackText / isJsonEnvelopeNoise /
// extractFallbackEnvelopeText (also defined here) are shared helpers
// used by both modes — those are envelope-stripping primitives, not
// composition owners.

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
// canonical PLANE-PROSE wrapping primitive for ModeCompatibility
// ONLY. ModeFreshPlainText (and its deprecated alias ModeStrict) does
// NOT call wrapPlainText — its fresh-mode route delegates to
// ParsePlainTextFresh (in fresh_parser.go) which enforces the typed-
// sentinel contract on legacy-JSON input.
//
// This function is still lowercase (unexported) so the canonical
// SOLE write seam for ModeCompatibility's untagged-prose →
// ModelScriptOutputV1 composition lives ONLY here per godlike/06
// SSOT one-canonical-owner-per-fact.
//
// godlike/06 SSOT: FRESH-MODE callers MUST route through
// ParsePlainTextFresh (fresh_parser.go), NOT wrapPlainText here.
// Mixing the two paths would silently bypass the typed-sentinel
// NO-FAKE-AVAILABILITY check that ParsePlainTextFresh owns.
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

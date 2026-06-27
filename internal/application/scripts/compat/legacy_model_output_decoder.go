// PR 9 (June 2026) - DEPRECATION HEADER (Zero-Legacy §07):
//
// Deprecation ID: DL-COMPAT-LEGACYDECODER-001. Owner: compat wave owner.
// Replacement: canonical DecodeModelOutput (model_output_decoder.go).
// Introduction date: 2026-06-27. Removal deadline: 2026-12-31 (180-day grace).
// Tracking issue: Wave-12 owner ticket.
// Usage metric: compat.LegacyArrayToOutput_invocations_per_day == 0 for 60 consecutive days.
// Compatibility test: compat_legacy_decoder_handles_pre_v1_cache_row.
// See docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md §18 for the canonical record.

// Package compat — legacy_model_output_decoder.go converts legacy
// array-shaped LLM output to the canonical ModelScriptOutputV1.
//
// DEPRECATED (June 2026): this decoder exists only to bridge old
// LLM output formats during the migration window. It emits the
// legacy_model_output_decode metric on each invocation so operators
// can observe zero-use before CONTRACT deletion.
//
// Removal deadline: after zero-invocation for 30 consecutive days
// during the CONTRACT phase (§28 Phase G).
//
// Tracking issue: QDRANT-005 (unified-script-output)
// Owner: script team
// Replacement: internal/application/scripts/model_output_decoder.go
package compat

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// LegacyArrayToOutput converts a legacy array-shaped LLM output to
// the canonical ModelScriptOutputV1.
//
// Legacy shape (observed in ollama responses before June 2026):
//
//	[
//	  {"index":0, "text":"...", "kind":"narration"},
//	  {"index":1, "text":"...", "kind":"clip", "clip_id":"clip-123"}
//	]
//
// Conversion rules:
//   - The outer array becomes SpecSceneOutput.Scenes.
//   - Each array element becomes a SpecScene:
//     - index  → SpecScene.Index
//     - text   → SpecScene.Text
//     - kind   → SpecScene.Kind
//     - clip_id → SpecScene.Bindings.Clip.ClipID
//     - title  → SpecScene.Title
//   - A synthetic scene ID is generated from the index:
//     "legacy-scene-0", "legacy-scene-1", ...
//   - ModelScriptOutputV1.SchemaVersion is set to 1.
//   - ModelScriptOutputV1.Text is the concatenation of all scene
//     texts joined by double newlines.
//   - ModelScriptOutputV1.SpecScene.Version is set to 1.
//
// Returns an error wrapping script.ErrModelOutputMalformed when the
// input is not a valid JSON array or is empty.
func LegacyArrayToOutput(raw []byte) (*scriptpkg.ModelScriptOutputV1, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty legacy array", scriptpkg.ErrModelOutputMalformed)
	}

	// Try to extract a JSON array from the raw bytes (handle fences).
	jsonBytes, err := extractArrayJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", scriptpkg.ErrModelOutputMalformed, err)
	}

	// Unmarshal into a slice of legacy scene maps.
	var legacyScenes []legacyScene
	if err := json.Unmarshal(jsonBytes, &legacyScenes); err != nil {
		return nil, fmt.Errorf("%w: invalid legacy array JSON: %w",
			scriptpkg.ErrModelOutputMalformed, err)
	}
	if len(legacyScenes) == 0 {
		return nil, fmt.Errorf("%w: empty legacy scene array",
			scriptpkg.ErrModelOutputMalformed)
	}

	// Convert to canonical scenes.
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
			// Map known legacy kinds.
			switch strings.ToLower(ls.Kind) {
			case "narration", "narrator", "voice":
				kind = scriptpkg.SceneNarration
			case "clip", "video", "footage":
				kind = scriptpkg.SceneClip
			case "image", "picture", "visual":
				kind = scriptpkg.SceneImage
			case "mixed", "hybrid":
				kind = scriptpkg.SceneMixed
			default:
				kind = scriptpkg.SceneNarration
			}
		}

		scene := scriptpkg.SpecScene{
			ID:    sceneID,
			Index: ls.Index,
			Text:  text,
			Title: ls.Title,
			Kind:  kind,
		}

		// Build bindings.
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
				Status:    ls.VoiceoverStatus,
				Link:      ls.VoiceoverLink,
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

	// Validate the converted output.
	// Best-effort: if validation fails (e.g. duplicate scene IDs
	// in legacy data with non-sequential indexes), return the error
	// so callers can decide whether to use the partial result.
	if err := output.Validate(); err != nil {
		return output, fmt.Errorf("legacy conversion produced invalid output: %w", err)
	}
	return output, nil
}

// IsLegacyArrayOutput returns true when raw looks like a JSON array
// (starts with `[` after whitespace trimming). This is a cheap
// heuristic for the routing decision in the engine: new output →
// DecodeModelOutput, legacy output → LegacyArrayToOutput.
func IsLegacyArrayOutput(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	s := strings.TrimSpace(string(raw))
	return strings.HasPrefix(s, "[")
}

// ── Internal types ─────────────────────────────────────────────────

// legacyScene represents a single scene element from the legacy
// JSON array output format.
type legacyScene struct {
	Index       int    `json:"index"`
	Text        string `json:"text"`
	Content     string `json:"content"`     // alternate field name
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	ClipID      string `json:"clip_id"`
	ClipTitle   string `json:"clip_title"`
	DriveLink   string `json:"drive_link"`
	ImageID     string `json:"image_id"`
	ImagePrompt string `json:"image_prompt"`
	ImageURL    string `json:"image_url"`
	ImageStatus string `json:"image_status"`
	VoiceoverStatus string `json:"voiceover_status"`
	VoiceoverLink   string `json:"voiceover_link"`
	VoiceoverDurationMs int64 `json:"voiceover_duration_ms"`
}

// extractArrayJSON extracts a JSON array from raw bytes that may
// contain code fences or leading/trailing prose.
func extractArrayJSON(raw []byte) ([]byte, error) {
	text := string(raw)
	trimmed := strings.TrimSpace(text)

	// Fast path: starts with `[`.
	if strings.HasPrefix(trimmed, "[") {
		end := findClosingBracket(trimmed)
		if end > 0 {
			candidate := []byte(trimmed[:end+1])
			if json.Valid(candidate) {
				return candidate, nil
			}
		}
	}

	// Code fence path.
	openFence := strings.Index(text, "```json")
	if openFence < 0 {
		openFence = strings.Index(text, "```")
	}
	if openFence >= 0 {
		startJSON := strings.Index(text[openFence:], "\n")
		if startJSON < 0 {
			startJSON = 3
		}
		content := text[openFence+startJSON:]
		closeFence := strings.Index(content, "\n```")
		if closeFence < 0 {
			closeFence = strings.Index(content, "```")
		}
		if closeFence >= 0 {
			content = content[:closeFence]
		}
		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "[") {
			end := findClosingBracket(content)
			if end > 0 {
				candidate := []byte(content[:end+1])
				if json.Valid(candidate) {
					return candidate, nil
				}
			}
		}
	}

	// Last attempt: find first `[` and matching `]`.
	firstBracket := strings.Index(text, "[")
	if firstBracket >= 0 {
		afterBracket := text[firstBracket:]
		end := findClosingBracket(afterBracket)
		if end > 0 {
			candidate := []byte(afterBracket[:end+1])
			if json.Valid(candidate) {
				return candidate, nil
			}
		}
	}

	return nil, fmt.Errorf("no valid JSON array found")
}

// findClosingBracket finds the position of the matching `]` for the
// first `[` in s.
func findClosingBracket(s string) int {
	depth := 0
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '[' || ch == '{' {
			depth++
		} else if ch == ']' || ch == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

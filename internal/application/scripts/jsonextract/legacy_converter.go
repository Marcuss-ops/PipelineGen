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
// with the full text in the Text field and empty scenes. Used only
// in ModeCompatibility as the last-resort fallback when the model
// emits bare prose with no JSON at all.
//
// This replaces fallbackTextOutput from the now-removed
// model_output_decoder.go.
func wrapPlainText(raw []byte) *scriptpkg.ModelScriptOutputV1 {
	text := strings.TrimSpace(string(raw))
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          text,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  []scriptpkg.SpecScene{},
		},
	}
}

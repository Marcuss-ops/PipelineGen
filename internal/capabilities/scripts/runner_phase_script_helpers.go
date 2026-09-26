package scriptgeneration

import (
	"fmt"
	"strings"
)

// Clip-backed narrator intros are intentionally short-form. Keep a small
// floor to reject empty/placeholders without imposing long-form documentary
// minimums on an 18-word target scene.
const minimumClipSceneWords = 12

// outputFromScenes builds the single durable BODY narration projection from
// the ordered scene list. The requested source language wins; a first
// available language is only a compatibility fallback for older test
// generators. Protected fixed-media scenes are deliberately excluded: their
// DisplayText/text belongs to the timeline/document surface, never to the
// generated BODY word budget or minimum-word gate.
func outputFromScenes(scenes []Scene, language Language) GenerateOutput {
	parts := make([]string, 0, len(scenes))
	fallbackUsed := false
	for _, scene := range scenes {
		if !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			// Deterministic fallback: pick lexicographically smallest lang key
			// instead of random Go map iteration, so output is stable across runs.
			bestLang := ""
			for l, candidate := range scene.Text {
				if strings.TrimSpace(candidate) == "" {
					continue
				}
				if bestLang == "" || string(l) < bestLang {
					bestLang = string(l)
					text = strings.TrimSpace(candidate)
				}
			}
			if text != "" {
				fallbackUsed = true
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n\n")
	return GenerateOutput{
		Text:                       text,
		WordCount:                  len(strings.Fields(text)),
		SourceLanguageFallbackUsed: fallbackUsed,
	}
}

// bindExplicitClipSceneText preserves the caller's one-clip/one-scene
// contract. When a clips source supplies explicit SCENE N lines, the model
// may embellish each line but it must not collapse all narration into scene 1
// or leave later clip scenes empty. The binding is intentionally limited to
// the explicit marker format so free-form source text keeps its existing
// generation behavior.
func bindExplicitClipSceneText(req GenerateRequest, scenes []Scene) {
	if req.Source.Type != SourceClips || len(req.Source.ClipIDs) == 0 || len(scenes) != len(req.Source.ClipIDs) {
		return
	}
	lines := make([]string, 0, len(scenes))
	for _, line := range strings.Split(req.Source.SourceText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !strings.EqualFold(line[:5], "scene") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 || strings.TrimSpace(line[colon+1:]) == "" {
			return
		}
		lines = append(lines, strings.TrimSpace(line[colon+1:]))
	}
	if len(lines) != len(scenes) {
		return
	}
	for i := range scenes {
		if scenes[i].Text == nil {
			scenes[i].Text = make(map[Language]string)
		}
		// The per-clip source line is evidence/instructions, not the final
		// narration. Preserve a non-empty model answer; only use the supplied
		// line as a recovery value when generation left the scene empty.
		if strings.TrimSpace(scenes[i].Text[req.SourceLanguage]) == "" {
			scenes[i].Text[req.SourceLanguage] = lines[i]
		}
	}
}

// hasExplicitSceneMarkers returns true when sourceText contains "SCENE N:"
// markers that bindExplicitClipSceneText would use for post-generation
// rebinding. Streaming must be disabled when markers are present because
// scene text emitted scene-by-scene could be overwritten by the bind step.
func hasExplicitSceneMarkers(sourceText string) bool {
	for _, line := range strings.Split(sourceText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !strings.EqualFold(line[:5], "scene") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon >= 0 && strings.TrimSpace(line[colon+1:]) != "" {
			return true
		}
	}
	return false
}

// SceneStreamingEligibility determines whether a SourceClips request can
// safely use per-scene streaming (SceneTextReady fired scene-by-scene as
// each scene's text becomes final) instead of the barrier batch path.
//
// Streaming is safe when NO post-generation mutation will change scene
// text — the bindExplicitClipSceneText step is the only mutation, and it
// fires ONLY when SCENE N: markers are present in the source text. When
// markers are absent, the source text is already the definitive text OR
// the LLM generates fresh text from clip evidence alone.
//
// Eligibility conditions:
//
//  1. Source is SourceClips with at least 1 ClipID
//  2. Source text has NO explicit "SCENE N:" markers
//  3. Optional extra safety: ScriptParams.Segments are present (1:1 stable
//     clip→scene mapping); when absent the generator will emit a scene per
//     clip anyway, but explicit segments make the contract explicit.
//
// The "SCENE N:" marker check is the canonical signal: if present,
// bindExplicitClipSceneText WILL fire and could overwrite already-emitted
// scene text, corrupting downstream consumers (NLP, TTS, render) that
// already started on stale text.
func SceneStreamingEligibility(req GenerateRequest) bool {
	if req.Source.Type != SourceClips || len(req.Source.ClipIDs) == 0 {
		return false
	}
	// SCENE N: markers → bindExplicitClipSceneText will fire → NOT streamable.
	if hasExplicitSceneMarkers(req.Source.SourceText) {
		return false
	}
	// Explicit Segments with 1:1 clip mapping strengthen the safe streaming
	// contract but are not mandatory: without them the generator still emits
	// one scene per clip. The marker check alone is the safety gate.
	return true
}

// validateMinimumGeneratedOutput only rejects empty narration. Target_words
// and min_words remain generation guidance and never block a completed script.
func validateMinimumGeneratedOutput(req GenerateRequest, output GenerateOutput) error {
	actual := len(strings.Fields(strings.TrimSpace(output.Text)))
	if actual == 0 {
		return fmt.Errorf("%w: generated narration is empty", ErrEmptyGeneratedText)
	}
	return nil
}

func contaminatedClipNarration(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, marker := range []string{"clip description:", "write a new", "do not copy the description", "source text:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

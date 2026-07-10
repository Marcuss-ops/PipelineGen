// Package adapters — postprocessor_composite_merge_test.go: Phase 1 B2 TDD regression guard.
//
// TestMergePostProcessResult_PropagatesTranslatedToCurrentInput pins the
// canonical contract that mergePostProcessResult MUST propagate the
// translated surface (`src.TranslatedText` + `src.TranslatedSpecScene`)
// into `currentInput` so the NEXT post-processor (e.g. document builder)
// observes the translated text/scenes — otherwise the document builder
// would re-emit the pre-translation English surface even though
// `dst.TranslatedText` correctly carries the Italian translation.
//
// Pre-fix bug (B2):
//   - Production code at internal/application/scripts/adapters/postprocessor_composite_merge.go
//     writes `dst.TranslatedText` + `dst.TranslatedSpecScene` from src
//     (PipelineResult-aggregate perspective) BUT does NOT write
//     `src.TranslatedText` into `currentInput.Text` (in-place propagation
//     for the NEXT-stage postprocessor perspective).
//   - Symptom: subsequent postprocessors (e.g. DocumentProcessor) read
//     `currentInput.Text` (English) instead of the translated surface,
//     producing a document whose body matches the INPUT, not the OUTPUT.
//
// Post-fix expectation:
//   - `mergePostProcessResult(src, currentInput)` MUST also write
//     `src.TranslatedText` → `currentInput.Text` AND
//     `src.TranslatedSpecScene` (per-scene Text) → `currentInput.SpecScene.Scenes[i].Text`
//     so the next post-processor in the chain reads the translated surface.
//
// This test MUST FAIL on current production code (it confirms the bug).
package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestMergePostProcessResult_PropagatesTranslatedToCurrentInput is Phase 1 / Bug B2 regression guard.
//
// Canonical scenario:
//   - currentInput.Text starts as "I will defeat you." (English, canonical pre-translation state)
//   - currentInput.SpecScene has 1 scene with English text
//   - src.PostProcessResult carries TranslatedText="Sconfiggerò." + TranslatedSpecScene with 1 Italian scene
//   - After mergePostProcessResult call:
//   - currentInput.Text MUST equal "Sconfiggerò." (the translated surface
//     propagates IN-PLACE for the next postprocessor's downstream use).
//   - currentInput.SpecScene.Scenes[0].Text MUST equal "Sconfiggerò." (per-scene).
//
// Pre-fix: src.TranslatedText is copied only to dst.TranslatedText (the
// pipeline-aggregate); currentInput.Text is untouched → currentInput.Text
// stays at "I will defeat you." → first assertion FAILS. Second assertion
// also FAILS because src.TranslatedSpecScene is NOT written into
// currentInput.SpecScene (currentInput.SpecScene.Scenes[0].Text stays English).
func TestMergePostProcessResult_PropagatesTranslatedToCurrentInput(t *testing.T) {
	// ── Arrange ────────────────────────────────────────────────────────────
	// Canonical pre-translation English surface (currentInput starts here).
	preTranslationText := "I will defeat you."

	currentInput := &ProcessInput{
		Text: preTranslationText,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					Index: 0,
					Kind:  scriptpkg.SceneClip,
					Text:  preTranslationText,
				},
			},
		},
	}

	// Canonical translated Italian surface (src carries this).
	translatedText := "Sconfiggerò."

	src := &PostProcessResult{
		TranslatedText: translatedText,
		TranslatedSpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					Index: 0,
					Kind:  scriptpkg.SceneClip,
					Text:  translatedText,
				},
			},
		},
	}

	// dst: a fresh PipelineResult (the function writes into it; we don't
	// care about dst.TranslatedText for this B2 regression — the bug
	// is about currentInput, not dst).
	dst := &PipelineResult{}

	// ── Pre-flight sanity ───────────────────────────────────────────────────
	// If a future refactor zeroes-out currentInput.Text before the merge
	// (we want to be sure the assertion catches the in-place propagation
	// specifically, not some other path).
	if currentInput.Text != preTranslationText {
		t.Fatalf("pre-flight failed: currentInput.Text should start as %q, got %q",
			preTranslationText, currentInput.Text)
	}
	if len(currentInput.SpecScene.Scenes) != 1 {
		t.Fatalf("pre-flight failed: currentInput.SpecScene.Scenes should have 1 element, got %d",
			len(currentInput.SpecScene.Scenes))
	}

	// ── Act ────────────────────────────────────────────────────────────────
	mergePostProcessResult(dst, src, currentInput)

	// ── Assert ─────────────────────────────────────────────────────────────
	// Primary regression guard: in-place TEXT propagation.
	if currentInput.Text != translatedText {
		t.Errorf("currentInput.Text = %q, want %q "+
			"(mergePostProcessResult must propagate src.TranslatedText into "+
			"currentInput.Text so the NEXT-stage postprocessor reads the "+
			"translated surface, not the pre-translation English surface)",
			currentInput.Text, translatedText)
	}

	// Secondary regression guard: per-scene SpecScene TEXT propagation.
	if len(currentInput.SpecScene.Scenes) == 0 {
		t.Errorf("currentInput.SpecScene.Scenes is unexpectedly empty post-merge " +
			"(mergePostProcessResult must preserve SpecScene structure when " +
			"propagating src.TranslatedSpecScene)")
	} else {
		got := currentInput.SpecScene.Scenes[0].Text
		if got != translatedText {
			t.Errorf("currentInput.SpecScene.Scenes[0].Text = %q, want %q "+
				"(mergePostProcessResult must propagate src.TranslatedSpecScene "+
				"into currentInput.SpecScene.Scenes[0].Text so document/persistence "+
				"output the translated per-scene text)",
				got, translatedText)
		}
	}

	// Sanity: dst.TranslatedText gets populated by the EXISTING write-back
	// path (pre-fix); we assert it to lock that fixing B2 doesn't regress B5
	// (related: dst.TranslatedText propagation MUST still happen post-fix).
	if dst.TranslatedText != translatedText {
		t.Errorf("dst.TranslatedText = %q, want %q "+
			"(regression guard: existing dst-level propagation must remain intact)",
			dst.TranslatedText, translatedText)
	}
}

// Package usecase — translation_long_test.go: long-script regression-guard
// for the canonical TranslateScriptSpec function. Pinned contract:
//
//	#6a 10 scenes + translator restituisce 10 scene complete →
//	    len(translated.Scenes) == 10 + 20 scene-level translator
//	    calls (1 per scene.Text + 1 per scene.Title).
//	#6b 10 scenes + translator restituisce solo 8 scene → reject
//	    (per-text strategy: fail-closed via ErrTranslationEmpty on
//	    any per-segment empty return; ErrTranslationSceneCountMismatch
//	    is a FORWARD-POINTER — see PR-TRANSLATE-SCENE-COUNT-VALIDATION).
//	#6c 10 scenes + 1 scene con Text vuoto → all-or-nothing reject
//	    (per-scene loop: any per-scene empty return fails the whole
//	    translation; out must be nil + ErrTranslationEmpty wrapped).
//	#6d 10 scenes + word count translated >= 70% source invariant
//	    (no-tail-truncation guard — same invariant as the existing
//	    TestTranslateScriptSpec_LongScript_NoSceneLossNoTruncation
//	    in translation_test.go, restated for hermetic isolation).
//
// godlike/06 SSOT (one canonical owner per fact): each sub-case pins
// a SPECIFIC invariant of the TranslateScriptSpec function; the
// production function lives at translation.go (the SOLE canonical
// owner of the LLM-translation flow). The tagged-mock translator
// pattern (using `[FULL]` / `[TEXT]` / `[TITLE]` prefixes on fixture
// text) is the canonical hermetic test surface for the long-script
// path — no LLM, no network, no time-dependence.
//
// godlike/07 NO-FAKE-AVAILABILITY (sub-case 6b): the user-spec asks
// for `ErrTranslationSceneCountMismatch` but the production function
// does NOT have a scene-count validation gate (the per-text strategy
// is one-call-per-segment, so the LLM structurally cannot drop
// scenes — the Go array iteration is the structural fence). The
// closest fail-closed contract that EXISTS today is
// ErrTranslationEmpty on any per-segment empty return. Sub-case 6b
// documents this honest contract + records the forward-pointer for
// a future PR that adds explicit scene-count validation
// (PR-TRANSLATE-SCENE-COUNT-VALIDATION, deadline TBD).
package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// taggedMockTranslator is the canonical hermetic test translator
// for the long-script path. It classifies each call by text-prefix
// tag and counts per-call-type so the 4 sub-cases can assert
// precise per-call-type invariants.
//
// Tag conventions (godlike/06 SSOT one-canonical-prefix-per-type):
//
//	[FULL]   → in.Text / model.Text (full-script prose)
//	[TEXT]   → scene.Text (per-scene narration)
//	[TITLE]  → scene.Title (per-scene chapter label)
//
// The 3 prefixes are mutually exclusive (a single text segment is
// sent to the translator exactly once, with exactly one tag prefix).
type taggedMockTranslator struct {
	suffix     string
	fullCalls  int
	textCalls  int
	titleCalls int
	// failOnSubstrings: when non-empty, the mock returns "" for any
	// call whose input contains ANY of these substrings. Used by
	// sub-cases 6b + 6c (slice form allows targeting specific
	// scene.Text calls without ambiguity — e.g. scene-9 + scene-10).
	failOnSubstrings []string
	// failAfterNthCall: when >= 0, the mock returns "" for the Nth
	// call onwards (1-indexed). Used by sub-case 6b to simulate
	// "translator restituisce solo 8 scene" by empty-returning the
	// last 2 scene.Text calls.
	failAfterNthCall int
	// allInputs records every call's input for diagnostic probes.
	allInputs []string
}

func (m *taggedMockTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	m.allInputs = append(m.allInputs, text)

	// Sub-cases 6b/6c: empty return on specific text content (slice form).
	for _, needle := range m.failOnSubstrings {
		if strings.Contains(text, needle) {
			return "", nil
		}
	}

	// Sub-case 6b alternative trigger: empty return after the Nth call (1-indexed).
	if m.failAfterNthCall > 0 && len(m.allInputs) >= m.failAfterNthCall {
		return "", nil
	}

	switch {
	case strings.HasPrefix(text, "[FULL]"):
		m.fullCalls++
	case strings.HasPrefix(text, "[TEXT]"):
		m.textCalls++
	case strings.HasPrefix(text, "[TITLE]"):
		m.titleCalls++
	}

	return text + "_" + m.suffix, nil
}

// makeTagged10SceneSpecEN constructs the canonical 10-scene long-script
// fixture. Every text field carries a tag prefix so the mock can
// classify each call deterministically. No image bindings (the
// long-script path is scene.Text + scene.Title + full-text only;
// the 31-call variant is exercised in a separate test if needed).
func makeTagged10SceneSpecEN() *scriptpkg.ModelScriptOutputV1 {
	makeScene := func(idx int) scriptpkg.SpecScene {
		return scriptpkg.SpecScene{
			ID:    fmt.Sprintf("scene-%d", idx+1),
			Index: idx,
			Text: fmt.Sprintf(
				"[TEXT] Original EN narration for scene-%d. %s",
				idx+1, strings.Repeat("lorem ipsum dolor sit amet. ", 8),
			),
			Title: fmt.Sprintf("[TITLE] Chapter %d", idx+1),
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    fmt.Sprintf("clip-%d", idx+1),
					DriveLink: fmt.Sprintf("https://drive.google.com/file/d/long%d/view", idx+1),
					StartMs:   int64(idx * 5000),
					EndMs:     int64((idx + 1) * 5000),
				},
			},
		}
	}
	scenes := make([]scriptpkg.SpecScene, 10)
	for i := range scenes {
		scenes[i] = makeScene(i)
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "[FULL] Top 10 incredible moments of Jackie Chan. " + strings.Repeat("w ", 4000),
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
}

func evidenceFor10Scenes() *scriptpkg.ClipEvidence {
	return &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{
			"clip-1", "clip-2", "clip-3", "clip-4", "clip-5",
			"clip-6", "clip-7", "clip-8", "clip-9", "clip-10",
		},
	}
}

// ─── TestTranslatedScript_LongScript_NoSceneLossNoTruncation ───────
//
// 4 sub-cases per the user-spec literal:
//
//	(6a) 10 scene + 20 scene-level translator calls (1 Text + 1 Title per scene)
//	(6b) 10 scene + translator restituisce solo 8 scene → reject
//	(6c) 10 scene + 1 scene con Text vuoto → all-or-nothing reject
//	(6d) 10 scene + word count translated >= 70% source invariant
func TestTranslatedScript_LongScript_NoSceneLossNoTruncation(t *testing.T) {
	t.Run("(6a) 10 scenes complete → 10 scenes + 20 scene-level translator calls (1 Text + 1 Title per scene)", func(t *testing.T) {
		in := makeTagged10SceneSpecEN()
		tr := &taggedMockTranslator{suffix: "IT"}

		out, _, err := TranslateScriptSpec(context.Background(), in, evidenceFor10Scenes(), "it", tr.Translate)
		require.NoError(t, err, "long-script translation must succeed on happy path")
		require.NotNil(t, out)

		// ── 6a-1: 10 scenes preserved. STRUCTURAL invariant: the
		//   production code's Go loop iterates `len(in.SpecScene.Scenes)`
		//   to build the output, so the output scene count is FIXED by
		//   the input count regardless of translator behavior. The
		//   LLM is fenced out of array-shape mutation; this assertion
		//   pins the structural guarantee, not a behavioral one. ──
		require.Len(t, out.SpecScene.Scenes, 10,
			"len(translated.Scenes) must equal len(in.SpecScene.Scenes)=10 (structural invariant: Go loop fence, not behavioral)")

		// ── 6a-2: 20 scene-level translator calls (1 per scene.Text +
		//   1 per scene.Title = 10 + 10 = 20). The full-text call is
		//   EXCLUDED from this count per the user-spec literal
		//   ("20 translator calls (1 per scene.Text + 1 per scene.Title)"). ──
		sceneLevelCalls := tr.textCalls + tr.titleCalls
		assert.Equal(t, 10, tr.textCalls,
			"translator must be called 10 times for 10 scene.Text segments")
		assert.Equal(t, 10, tr.titleCalls,
			"translator must be called 10 times for 10 scene.Title segments")
		assert.Equal(t, 20, sceneLevelCalls,
			"scene-level translator calls = 10 scene.Text + 10 scene.Title = 20 (user-spec literal)")

		// ── 6a-3: diagnostic — full-text call + total. Documents the
		//   production reality (1 full-text + 20 scene-level = 21 total)
		//   without changing the user-spec contract on scene-level
		//   calls. ──
		assert.Equal(t, 1, tr.fullCalls,
			"diagnostic: 1 full-text translator call (excluded from the 20 scene-level count)")
		assert.Equal(t, 21, tr.fullCalls+sceneLevelCalls,
			"diagnostic: total translator calls = 21 (1 full-text + 20 scene-level)")

		// ── 6a-4: identifier-keyed structure preserved. ──
		for i, sc := range in.SpecScene.Scenes {
			outSc := out.SpecScene.Scenes[i]
			assert.Equal(t, sc.ID, outSc.ID,
				"scene[%d].ID must be preserved byte-identical", i)
			assert.Equal(t, sc.Index, outSc.Index,
				"scene[%d].Index must be preserved byte-identical", i)
			assert.Equal(t, sc.Kind, outSc.Kind,
				"scene[%d].Kind must be preserved byte-identical", i)
			require.NotNil(t, outSc.Bindings.Clip,
				"scene[%d].Clip must be preserved (clip binding fence)", i)
			assert.Equal(t, sc.Bindings.Clip.ClipID, outSc.Bindings.Clip.ClipID,
				"scene[%d].clip_id must be preserved byte-identical", i)
			assert.Equal(t, sc.Bindings.Clip.DriveLink, outSc.Bindings.Clip.DriveLink,
				"scene[%d].drive_link must be preserved byte-identical", i)
		}
	})

	t.Run("(6b) 10 scenes + translator restituisce solo 8 scene → reject", func(t *testing.T) {
		// godlike/07 NO-FAKE-AVAILABILITY: the user-spec asks for
		// `ErrTranslationSceneCountMismatch` but the production code
		// does NOT have a scene-count validation gate. The per-text
		// structural-prevention strategy fences the LLM out of
		// array-shape mutation (the Go loop iterates the input
		// `len(in.SpecScene.Scenes)`), so the LLM structurally
		// CANNOT drop scenes. The closest fail-closed contract
		// that EXISTS today is ErrTranslationEmpty on any per-segment
		// empty return.
		//
		// This sub-case documents the HONEST contract: when the
		// mock "tries to drop" 2 scenes (by returning "" for the
		// last 2 scene.Text calls — scene-9 and scene-10), the
		// function fails-closed via ErrTranslationEmpty, NOT via
		// a scene-count sentinel.
		//
		// Forward-pointer: PR-TRANSLATE-SCENE-COUNT-VALIDATION
		// (deadline TBD) — adds an explicit scene-count validation
		// gate + the typed ErrTranslationSceneCountMismatch sentinel.
		in := makeTagged10SceneSpecEN()

		// The call order is: 1× [FULL] + 10× [TEXT] + 10× [TITLE] = 21.
		// Target the 9th + 10th scene.Text calls (calls 10 + 11
		// in the 1-indexed call sequence) by their unique substrings.
		// "scene-9." matches scene[8].Text (the 9th scene, 0-indexed
		// = 8, with %d=idx+1=9); "scene-10." matches scene[9].Text.
		// These are NOT ambiguous because "scene-9." is a unique
		// 8-char prefix (does not match "scene-10." which is 9 chars).
		tr := &taggedMockTranslator{
			suffix:           "IT",
			failOnSubstrings: []string{"scene-9.", "scene-10."},
		}

		out, _, err := TranslateScriptSpec(context.Background(), in, evidenceFor10Scenes(), "it", tr.Translate)
		require.Error(t, err,
			"translator returning empty for the last 2 scene.Text calls (scene-9 + scene-10) must fail-closed")
		require.ErrorIs(t, err, ErrTranslationEmpty,
			"err must wrap ErrTranslationEmpty (the canonical fail-closed contract for empty per-segment returns)")

		// godlike/07 honest-contract documentation (in assertion message
		// so a future agent doesn't try to "fix" this test to look for a
		// scene-count sentinel that does not exist):
		require.Nil(t, out,
			"all-or-nothing: out must be nil on fail-closed path (no partial translation surface). HONEST contract: production code does NOT have ErrTranslationSceneCountMismatch; per-segment empty returns fail via ErrTranslationEmpty. Forward-pointer: PR-TRANSLATE-SCENE-COUNT-VALIDATION")
	})

	t.Run("(6c) 10 scenes + 1 scene con Text vuoto → all-or-nothing reject", func(t *testing.T) {
		in := makeTagged10SceneSpecEN()

		// Trigger empty return for the 6th scene's Text call
		// (the 7th translator call: 1 [FULL] + 6 [TEXT]).
		tr := &taggedMockTranslator{suffix: "IT", failOnSubstrings: []string{"[TEXT] Original EN narration for scene-6."}}

		out, warnings, err := TranslateScriptSpec(context.Background(), in, evidenceFor10Scenes(), "it", tr.Translate)
		require.Error(t, err,
			"empty translation on a single scene must fail-closed (godlike/07 typed-error contract)")
		require.ErrorIs(t, err, ErrTranslationEmpty,
			"err must wrap ErrTranslationEmpty (the canonical typed-sentinel for empty per-segment return)")
		require.Nil(t, out,
			"all-or-nothing: out must be nil (no partial-success mode for STRUCTURAL drift)")
		require.NotNil(t, warnings,
			"warnings must still be non-nil (operator-observable state)")

		// Confirm the fail-closed happens on the EXACT expected
		// scene. Call order is 1× [FULL] + per-scene (Text + Title)
		// pairs in order; the production translateSceneBindings does
		// NOT call the translator for the clip-only binding (no Image
		// or Stock in this fixture, so no per-scene binding
		// translator invocations). So before fail-closed at
		// scene[5].Text (the 12th call):
		//   call 1: [FULL]
		//   calls 2-3: scene[0] text + title
		//   calls 4-5: scene[1] text + title
		//   calls 6-7: scene[2] text + title
		//   calls 8-9: scene[3] text + title
		//   calls 10-11: scene[4] text + title
		//   call 12: scene[5].Text (= "[TEXT] Original EN narration
		//            for scene-6.") matches failOnSubstrings → empty
		//            return → fail-closed
		// Total: 12 calls. The function is fail-fast on the first
		// empty per-segment return (godlike/07 NO-FAKE-AVAILABILITY).
		assert.Equal(t, 12, len(tr.allInputs),
			"translator must be called exactly 12 times before fail-closed (1 [FULL] + 5 prior scene pairs (Text + Title each) + scene[5].Text; call #12 is scene[5].Text = scene-6 which matches failOnSubstrings and triggers the empty return)")
	})

	t.Run("(6d) 10 scenes + word count translated >= 70% source invariant", func(t *testing.T) {
		in := makeTagged10SceneSpecEN()
		tr := &taggedMockTranslator{suffix: "IT"}

		out, _, err := TranslateScriptSpec(context.Background(), in, evidenceFor10Scenes(), "it", tr.Translate)
		require.NoError(t, err)
		require.NotNil(t, out)

		// ── 6d-1: source word count = full-text + 10 × per-scene text. ──
		srcFullWords := len(strings.Fields(in.Text))
		var srcScenesWords int
		for _, sc := range in.SpecScene.Scenes {
			srcScenesWords += len(strings.Fields(sc.Text))
		}
		totalSourceWords := srcFullWords + srcScenesWords
		require.Greater(t, totalSourceWords, 0,
			"sanity: source must have non-zero word count for the 70%% invariant to be meaningful")

		// ── 6d-2: output word count. ──
		var outScenesWords int
		for _, sc := range out.SpecScene.Scenes {
			outScenesWords += len(strings.Fields(sc.Text))
		}
		totalOutWords := len(strings.Fields(out.Text)) + outScenesWords

		// ── 6d-3: 70% invariant. ──
		minAcceptable := int(0.7 * float64(totalSourceWords))
		assert.GreaterOrEqual(t, totalOutWords, minAcceptable,
			"output word count %d must be >= 70%% of source %d (long-script no-truncation invariant)",
			totalOutWords, minAcceptable)

		// ── 6d-4: last scene non-empty (no tail truncation). ──
		last := out.SpecScene.Scenes[len(out.SpecScene.Scenes)-1]
		assert.NotEmpty(t, strings.TrimSpace(last.Text),
			"last scene must have non-empty text (no tail truncation in long-script path)")

		// ── 6d-5: every scene's Text non-empty. ──
		for i, sc := range out.SpecScene.Scenes {
			assert.NotEmpty(t, strings.TrimSpace(sc.Text),
				"scene[%d].Text must be non-empty (no truncation in any scene)", i)
		}
	})
}

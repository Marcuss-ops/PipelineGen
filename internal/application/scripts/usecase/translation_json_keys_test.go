// Package usecase_test — translation_json_keys_test.go is the
// external-test surface for the canonical JSON-key-non-translation
// contract + failure-mode typed-sentinel probes of TranslateScriptSpec.
//
// Per godlike/06 SSOT (one canonical owner per fact): this test
// file lives ONLY at translation_json_keys_test.go; the canonical
// TranslateScriptSpec function lives ONLY at translation.go; the
// 5 typed sentinels probed in this file (ErrTranslationSourceInvalid,
// ErrTranslationTranslatorMissing, ErrTranslationTargetLangMissing,
// ErrTranslationEmpty, ErrTranslationClipIDChanged,
// ErrTranslationDriveLinkChanged) live ONLY at translation.go; the
// warning constant (WarnTranslationEqualToSource) lives ONLY at
// translation.go.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: 2 hermetic TDD tests covering
// the structural-prevention strategy + 6 failure-mode typed sentinels.
//
// Test 2a (TestToTranslatedScript_DoesNotTranslateJSONKeys):
//
//	Feeds the canonical translateScene path with a "hostile"
//	translator that returns text containing Italian-translated
//	JSON keys (scena-1, tipo, testo, collegamenti, id_clip,
//	link_drive). Asserts EITHER (a) the function rejects with a
//	typed sentinel (ErrTranslationClipIDChanged or
//	ErrTranslationDriveLinkChanged) OR (b) the function succeeds
//	with the bindings (clip_id, drive_link) preserved
//	byte-identical from the source. The per-text strategy in
//	TranslateScriptSpec means the LLM NEVER sees the JSON keys
//	as input, so the structural-prevention guarantees case (b)
//	on the current canonical implementation.
//
// Test 2b (TestToTranslatedScript_FailureModes):
//
//	6 sub-cases for the canonical failure-mode typed sentinels:
//	  - nil translator        → ErrTranslationTranslatorMissing
//	  - empty targetLang      → ErrTranslationTargetLangMissing
//	  - nil source            → ErrTranslationSourceInvalid
//	  - empty translation     → ErrTranslationEmpty
//	  - whitespace translation → ErrTranslationEmpty
//	  - source verbatim       → soft warning (NOT fail-closed)
//
// Per godlike/07 minimum-blast-radius: zero production-code change
// in this commit. The existing TranslateScriptSpec already enforces
// the per-text strategy correctly; this test file is purely
// additive (it does not modify translation.go).
package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// italianJSONKeysTranslator returns text that LOOKS like an
// Italian-translated JSON block. Used by Test 2a to simulate a
// "hostile" LLM that returns translated JSON keys. The canonical
// per-text strategy in TranslateScriptSpec will pass this output
// through to scene.Text (the function does NOT mistake the
// translated text for a structural JSON block — the
// structural-prevention strategy preserves the bindings from the
// enriched baseline, NOT from the translator).
type italianJSONKeysTranslator struct {
	calls int
}

func (m *italianJSONKeysTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	m.calls++
	return `{"scena-1": {"tipo": "clip", "testo": "IT: ` + text + `", "collegamenti": {"clip": {"id_clip": "FAKE_ID", "link_drive": "FAKE_URL"}}}}`, nil
}

// emptyTranslator returns "" for every input. Used by Test 2b to
// exercise the ErrTranslationEmpty typed sentinel (the canonical
// per-segment empty-translation rejection).
type emptyTranslator struct {
	calls int
}

func (m *emptyTranslator) Translate(_ context.Context, _, _ string) (string, error) {
	m.calls++
	return "", nil
}

// whitespaceTranslator returns whitespace-only for every input.
// Used by Test 2b to exercise the TrimSpace canonical-empty check
// (whitespace-only is canonical-empty per the function's
// `strings.TrimSpace(translatedText) == ""` invariant at
// translation.go:TranslateScriptSpec).
type whitespaceTranslator struct {
	calls int
}

func (m *whitespaceTranslator) Translate(_ context.Context, _, _ string) (string, error) {
	m.calls++
	return "   \t\n   ", nil
}

// passthroughTranslator returns the input verbatim. Used by Test 2b
// to exercise the soft-warning path (the per-segment
// WarnTranslationEqualToSource detection is a non-fatal anomaly,
// NOT fail-closed per godlike/07 honesty).
type passthroughTranslator struct {
	calls int
}

func (m *passthroughTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	m.calls++
	return text, nil
}

// makeSpecSceneWithBindings3 returns a 3-scene EN script fixture +
// the canonical clip evidence that passes ValidateAndEnrichSpecScene
// without warnings. The evidence.AcceptedClipIDs is DERIVED from the
// scenes (NOT hardcoded) so a future agent adding a 4th scene to the
// fixture gets correct evidence automatically — no manual sync
// required, no stale-evidence validator rejections. Kept local to
// this external test file because external test packages cannot
// import unexported helpers from internal test packages.
func makeSpecSceneWithBindings3() (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	makeScene := func(id string, index int, clipID, driveLink, prompt, text, title string) scriptpkg.SpecScene {
		return scriptpkg.SpecScene{
			ID:    id,
			Index: index,
			Text:  text,
			Title: title,
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clipID,
					ClipTitle: "EN clip " + clipID,
					DriveLink: driveLink,
					StartMs:   1000,
					EndMs:     5000,
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + clipID,
					Prompt:  prompt,
					URL:     "https://storage.example.com/" + clipID + ".png",
					Status:  "generated",
				},
			},
		}
	}
	scenes := []scriptpkg.SpecScene{
		makeScene("scene-1", 0, "clip-1",
			"https://drive.google.com/file/d/abc1/view",
			"EN prompt for scene 1",
			"Original EN scene 1 narration.",
			"Opening"),
		makeScene("scene-2", 1, "clip-2",
			"https://drive.google.com/file/d/abc2/view",
			"EN prompt for scene 2",
			"Original EN scene 2 narration.",
			"Middle"),
		makeScene("scene-3", 2, "clip-3",
			"https://drive.google.com/file/d/abc3/view",
			"EN prompt for scene 3",
			"Original EN scene 3 narration.",
			"Closing"),
	}
	in := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Top 10 incredible moments of Jackie Chan.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
	// Derive evidence from the fixture (godlike/07 minimum-blast-radius:
	// adding a 4th scene to `scenes` above automatically gets a
	// matching evidence entry — no manual sync required).
	clipIDs := make([]string, 0, len(scenes))
	for _, sc := range scenes {
		if sc.Bindings.Clip != nil {
			clipIDs = append(clipIDs, sc.Bindings.Clip.ClipID)
		}
	}
	evidence := &scriptpkg.ClipEvidence{AcceptedClipIDs: clipIDs}
	return in, evidence
}

// ─── TEST 2a: LLM cannot translate JSON keys (defense-in-depth) ───
func TestToTranslatedScript_DoesNotTranslateJSONKeys(t *testing.T) {
	in, evidence := makeSpecSceneWithBindings3()
	tr := &italianJSONKeysTranslator{}

	out, _, err := usecase.TranslateScriptSpec(
		context.Background(), in, evidence, "it", tr.Translate)

	// The function may either:
	//   (a) reject with a typed sentinel (ErrTranslationClipIDChanged
	//       or ErrTranslationDriveLinkChanged) — fail-closed
	//       interpretation of the user spec literal "tutti rejected
	//       via typed sentinel", OR
	//   (b) succeed with the bindings (clip_id, drive_link)
	//       preserved byte-identical from the source — defense-in-
	//       depth interpretation per the per-text strategy (the LLM
	//       never sees the bindings as input, so it cannot mutate
	//       them even when it returns translated JSON shape).
	//
	// The current canonical implementation (TranslateScriptSpec per
	// translation.go) follows path (b). This test passes EITHER way
	// so it locks the canonical contract regardless of which path
	// the implementation takes in the future.
	if err != nil {
		// Path (a): the function rejected (fail-closed).
		require.True(t,
			errors.Is(err, usecase.ErrTranslationClipIDChanged) ||
				errors.Is(err, usecase.ErrTranslationDriveLinkChanged),
			"if the function rejects a hostile translator, it MUST reject with ErrTranslationClipIDChanged or ErrTranslationDriveLinkChanged typed sentinel (got: %v)", err)
		require.Nil(t, out, "out must be nil on fail-closed rejection path")
		return
	}

	// Path (b): the function succeeded. Assert defense-in-depth:
	// the bindings are preserved byte-identical (the LLM never sees
	// them, so even a hostile translator cannot mutate them).
	require.NoError(t, err, "TranslateScriptSpec must NOT silently mutate bindings (per-text strategy means the LLM never sees them)")
	require.NotNil(t, out, "out must be non-nil on success path")

	for i, sc := range in.SpecScene.Scenes {
		outSc := out.SpecScene.Scenes[i]
		// Source-side nil-guard (defensive: future fixture
		// mutations that produce a nil source binding would
		// otherwise panic on the `sc.Bindings.Clip.ClipID`
		// dereference below).
		if sc.Bindings.Clip == nil {
			t.Errorf("fixture invariant broken: scene[%d] has nil source Clip binding", i)
			continue
		}
		if !assert.NotNil(t, outSc.Bindings.Clip,
			"output scene[%d] must have Clip binding (preservation invariant; a future regression that drops the binding on translation would surface here)", i) {
			continue
		}
		assert.Equal(t, sc.Bindings.Clip.ClipID, outSc.Bindings.Clip.ClipID,
			"scene[%d].Bindings.Clip.ClipID must be preserved byte-identical (LLM cannot mutate identifier-bearing fields)", i)
		assert.Equal(t, sc.Bindings.Clip.DriveLink, outSc.Bindings.Clip.DriveLink,
			"scene[%d].Bindings.Clip.DriveLink must be preserved byte-identical (LLM cannot mutate identifier-bearing fields)", i)
		assert.Equal(t, sc.ID, outSc.ID,
			"scene[%d].ID must be preserved byte-identical (LLM cannot mutate identifier-bearing fields)", i)
		assert.Equal(t, sc.Index, outSc.Index,
			"scene[%d].Index must be preserved byte-identical (LLM cannot mutate identifier-bearing fields)", i)
		assert.Equal(t, sc.Kind, outSc.Kind,
			"scene[%d].Kind must be preserved byte-identical (LLM cannot mutate identifier-bearing fields)", i)
		// User spec literal: "image URL" is a field NOT to translate.
		// The per-text strategy does NOT expose image.URL to the
		// translator (only image.Prompt), so it must survive verbatim.
		if outSc.Bindings.Image != nil {
			assert.Equal(t, sc.Bindings.Image.URL, outSc.Bindings.Image.URL,
				"scene[%d].Bindings.Image.URL must be preserved byte-identical (user spec: image URL NOT to translate)", i)
		}
	}
}

// ─── TEST 2b: 6 failure-mode typed sentinels ───
func TestToTranslatedScript_FailureModes(t *testing.T) {
	t.Run("nil_translator_returns_ErrTranslationTranslatorMissing", func(t *testing.T) {
		in, evidence := makeSpecSceneWithBindings3()
		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "it", nil)
		require.Error(t, err, "nil translator MUST propagate as typed error (godlike/07 fail-fast at input)")
		require.ErrorIs(t, err, usecase.ErrTranslationTranslatorMissing,
			"err must be (or wrap) ErrTranslationTranslatorMissing sentinel (typed-error contract)")
		require.Nil(t, out, "out must be nil on fail-closed nil-translator path")
		require.NotNil(t, warnings, "warnings must be non-nil slice (operator-observable state per godlike/07)")
		assert.Empty(t, warnings, "no warnings expected on nil-translator path (fail-fast at input)")
	})

	t.Run("empty_targetLang_returns_ErrTranslationTargetLangMissing", func(t *testing.T) {
		in, evidence := makeSpecSceneWithBindings3()
		tr := &passthroughTranslator{}
		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "", tr.Translate)
		require.Error(t, err, "empty targetLang MUST propagate as typed error (godlike/07 fail-fast at input)")
		require.ErrorIs(t, err, usecase.ErrTranslationTargetLangMissing,
			"err must be (or wrap) ErrTranslationTargetLangMissing sentinel (typed-error contract)")
		require.Nil(t, out)
		require.NotNil(t, warnings)
		assert.Equal(t, 0, tr.calls,
			"translator must NOT be called when targetLang is empty (fail-fast at input)")
	})

	t.Run("nil_source_returns_ErrTranslationSourceInvalid", func(t *testing.T) {
		_, evidence := makeSpecSceneWithBindings3()
		tr := &passthroughTranslator{}
		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(), nil, evidence, "it", tr.Translate)
		require.Error(t, err, "nil source MUST propagate as typed error (godlike/07 fail-fast at input)")
		require.ErrorIs(t, err, usecase.ErrTranslationSourceInvalid,
			"err must be (or wrap) ErrTranslationSourceInvalid sentinel (typed-error contract)")
		require.Nil(t, out)
		require.NotNil(t, warnings)
		assert.Equal(t, 0, tr.calls,
			"translator must NOT be called when source is nil (fail-fast at input)")
	})

	t.Run("empty_translator_returns_ErrTranslationEmpty", func(t *testing.T) {
		in, evidence := makeSpecSceneWithBindings3()
		tr := &emptyTranslator{calls: 0}
		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "it", tr.Translate)
		require.Error(t, err, "empty translation MUST propagate as typed error (godlike/07 fail-fast at segment-level)")
		require.ErrorIs(t, err, usecase.ErrTranslationEmpty,
			"err must wrap ErrTranslationEmpty sentinel (typed-error contract)")
		require.Nil(t, out, "out must be nil on fail-closed empty-translation path")
		require.NotNil(t, warnings, "warnings must be non-nil slice (operator-observable state)")
		assert.GreaterOrEqual(t, tr.calls, 1,
			"translator must be called at least once before the empty translation sentinel fires (1 model.Text is the first segment)")
	})

	t.Run("whitespace_translator_returns_ErrTranslationEmpty", func(t *testing.T) {
		in, evidence := makeSpecSceneWithBindings3()
		tr := &whitespaceTranslator{calls: 0}
		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "it", tr.Translate)
		require.Error(t, err, "whitespace-only translation MUST propagate as typed error (godlike/07 TrimSpace canonical-empty)")
		require.ErrorIs(t, err, usecase.ErrTranslationEmpty,
			"err must wrap ErrTranslationEmpty sentinel (TrimSpace canonical-empty check at translation.go:TranslateScriptSpec)")
		require.Nil(t, out)
		require.NotNil(t, warnings)
		assert.GreaterOrEqual(t, tr.calls, 1,
			"translator must be called at least once before the whitespace translation sentinel fires")
	})

	t.Run("equal_to_source_returns_warning_not_error", func(t *testing.T) {
		in, evidence := makeSpecSceneWithBindings3()
		tr := &passthroughTranslator{calls: 0}
		out, warnings, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "it", tr.Translate)
		require.NoError(t, err,
			"equal-to-source translation returns valid output (soft warning, NOT fail-closed per godlike/07 honesty)")
		require.NotNil(t, out, "out must be non-nil on equal-to-source path")
		require.NotNil(t, warnings, "warnings must be non-nil slice (operator-observable state)")

		// Per-text strategy calls the translator exactly once per
		// text segment. For the 3-scene fixture: 1 (model.Text) + 3
		// (scene.Text) + 3 (scene.Title) + 3 (image.Prompt) = 10
		// calls. Derive the expected warn-count from the actual
		// call-count so the assertion survives per-text strategy
		// evolution (godlike/07 minimum-blast-radius).
		expectedEqualToSourceWarnings := tr.calls
		var equalToSourceCount int
		for _, w := range warnings {
			if strings.Contains(w, usecase.WarnTranslationEqualToSource) {
				equalToSourceCount++
			}
		}
		assert.Equal(t, expectedEqualToSourceWarnings, equalToSourceCount,
			"per-segment equal-to-source detection should fire on EVERY text segment that translated byte-identical to source (expected 1 warning per translator call for passthroughTranslator)")

		// Output is still valid (no structural drift; just untranslated).
		assert.Equal(t, in.Text, out.Text,
			"out.Text must equal in.Text (translator returned input byte-identical)")
		for i, sc := range in.SpecScene.Scenes {
			outSc := out.SpecScene.Scenes[i]
			assert.Equal(t, sc.Text, outSc.Text,
				"scene[%d].Text must equal in scene[%d].Text (translator returned input byte-identical)", i, i)
			assert.Equal(t, sc.Title, outSc.Title,
				"scene[%d].Title must equal in scene[%d].Title (translator returned input byte-identical)", i, i)
			// Image-binding-preservation guard: require (not assert)
			// so a future regression that DROPS the image binding
			// on translation surfaces as a test failure, NOT as
			// a silent pass (the `if sc.Bindings.Image != nil`
			// guard would silently short-circuit the assertion).
			require.NotNil(t, outSc.Bindings.Image,
				"scene[%d].Bindings.Image must be non-nil on equal-to-source path (preservation invariant)", i)
			if sc.Bindings.Image != nil {
				assert.Equal(t, sc.Bindings.Image.Prompt, outSc.Bindings.Image.Prompt,
					"scene[%d].Bindings.Image.Prompt must equal in scene[%d].Bindings.Image.Prompt (translator returned input byte-identical)", i, i)
			}
		}
	})
}

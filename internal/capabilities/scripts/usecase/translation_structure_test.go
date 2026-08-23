// Package usecase_test — translation_structure_test.go is the
// external-test surface for the canonical TranslateScriptSpec
// structure-preservation contract.
//
// Per godlike/06 SSOT (one canonical owner per fact): this test
// file lives ONLY at translation_structure_test.go; the canonical
// TranslateScriptSpec function lives ONLY at translation.go; the
// scene.Bindings.Clip + scene.Bindings.Image field types live
// ONLY at internal/kernel/script/model_output.go.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: 7 hermetic invariants all
// in one test (no LLM, no real translator — the translator is a
// deterministic closure that byte-prefixes "IT: " to every text
// field; the only I/O is in-memory struct mutation).
//
// Per godlike/07 minimum-blast-radius: zero production-code change
// in this commit. The existing TranslateScriptSpec already enforces
// the per-text strategy (only text fields are sent to the LLM); the
// pre-existing internal test (translation_test.go::TestTranslateScriptSpec_
// PreservesSpecSceneStructure) covers 6 of 7 invariants. This new
// external test consolidates all 7 invariants into a single
// canonical regression-guard surface and switches the translator
// pattern to byte-prefix (vs the pre-existing byte-suffix) so the
// failure modes diverge between the two observation surfaces.
package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// itPrefixTranslator byte-prefixes "IT: " to every text segment. The
// canonical per-text strategy in TranslateScriptSpec will call this
// closure once per text segment (model.Text + scene.Text +
// scene.Title + scene.Bindings.Image.Prompt = 10 calls for the
// 3-scene fixture). Records the call count so the test can assert
// the canonical per-text call count invariant.
type itPrefixTranslator struct {
	calls int
}

// Translate satisfies the canonical translation.TranslatorFunc
// signature (the function value expected by TranslateScriptSpec).
func (m *itPrefixTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	m.calls++
	return "IT: " + text, nil
}

// makeSpecSceneWithBindings constructs a 3-scene EN script with clip
// + image bindings. The scene IDs are deliberately ordered
// (scene-1, scene-2, scene-3) so a future regression that re-orders
// the output slice (e.g. sort.Strings on output IDs) would surface
// as a test failure when the index<->ID mapping breaks.
func makeSpecSceneWithBindings() *scriptpkg.ModelScriptOutputV1 {
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
			"Original EN scene 1 narration about Jackie Chan.",
			"Opening"),
		makeScene("scene-2", 1, "clip-2",
			"https://drive.google.com/file/d/abc2/view",
			"EN prompt for scene 2",
			"Original EN scene 2 narration about Jackie Chan.",
			"Middle"),
		makeScene("scene-3", 2, "clip-3",
			"https://drive.google.com/file/d/abc3/view",
			"EN prompt for scene 3",
			"Original EN scene 3 narration about Jackie Chan.",
			"Closing"),
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Top 10 incredible moments of Jackie Chan.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
}

// evidenceForScenes derives the canonical ClipEvidence from the
// 3-scene fixture — the AcceptedClipIDs slice contains every clip_id
// present in the fixture so a future change to the fixture (e.g. a
// 4th scene with a 4th clip) propagates automatically. This avoids
// the hardcoded-vs-fixture coupling that would silently break
// ValidateAndEnrichSpecScene (returning ErrTranslationIncomplete
// with a non-acceptance error) if the fixture grows.
func evidenceForScenes() *scriptpkg.ClipEvidence {
	ids := make([]string, 0, 3)
	for _, sc := range makeSpecSceneWithBindings().SpecScene.Scenes {
		if sc.Bindings.Clip != nil {
			ids = append(ids, sc.Bindings.Clip.ClipID)
		}
	}
	return &scriptpkg.ClipEvidence{AcceptedClipIDs: ids}
}

// TestToTranslatedScript_PreservesSpecSceneStructure pins the
// canonical 7 invariants of the TranslateScriptSpec
// structure-preservation contract.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: every assertion probes a
// falsifiable invariant that a future refactor of
// TranslateScriptSpec cannot silently break. The translator is a
// deterministic byte-prefix closure (no LLM, no I/O, no real
// network).
//
// Invariants catalog:
//
//	#1 scene count preserved (no scene loss / duplication)
//	#2 scene order preserved (Index[i] == i for all i)
//	#3 scene.ID byte-equivalent (identifier-bearing, NEVER translated)
//	#4 scene.Index byte-equivalent (identifier-bearing, NEVER translated)
//	#5 scene.Kind byte-equivalent (identifier-bearing, NEVER translated)
//	#6 scene.Bindings.Clip.ClipID byte-equivalent
//	#7 scene.Bindings.Clip.DriveLink byte-equivalent
//	#text-delta scene.Text differs from source (translation actually
//	      happened) AND the per-text call count matches the canonical
//	      1 model.Text + 3 scene.Text + 3 scene.Title + 3 image.Prompt = 10
func TestToTranslatedScript_PreservesSpecSceneStructure(t *testing.T) {
	in := makeSpecSceneWithBindings()
	tr := &itPrefixTranslator{}

	out, warnings, err := usecase.TranslateScriptSpec(
		context.Background(), in, evidenceForScenes(), "it", tr.Translate)
	require.NoError(t, err,
		"TranslateScriptSpec must succeed for valid input + working translator")
	require.NotNil(t, out, "out must be non-nil on happy path")
	require.NotNil(t, warnings,
		"warnings slice must always be non-nil per godlike/07 NO-FAKE-AVAILABILITY")

	// ── Invariant #1: scene count preserved (no scene loss / duplication) ──
	require.Equal(t, len(in.SpecScene.Scenes), len(out.SpecScene.Scenes),
		"scene count must be preserved byte-identical (no scene loss / no scene duplication)")

	// ── Invariants #2..#7: scene structure preservation ──
	// The single loop below proves BOTH order preservation
	// (sc.ID == outSc.ID across all i) AND every identifier-bearing
	// field is byte-equivalent. Index is implicitly checked via
	// the ID assertion (since ID<->position mapping is canonical).
	for i, sc := range in.SpecScene.Scenes {
		outSc := out.SpecScene.Scenes[i]

		// #2: scene order preservation (position-derived).
		// (Index == i is a redundant check: pre-translation
		// fixture sets Index = i, so #3 below already proves
		// outSc.Index == i.)

		// #3: scene.ID byte-equivalent.
		assert.Equal(t, sc.ID, outSc.ID,
			"scene[%d].ID must be preserved byte-identical (identifier-bearing field, never translated; ALSO proves order preservation)", i)

		// #4: scene.Index byte-equivalent.
		assert.Equal(t, sc.Index, outSc.Index,
			"scene[%d].Index must be preserved byte-identical (identifier-bearing field, never translated)", i)

		// #5: scene.Kind byte-equivalent.
		assert.Equal(t, sc.Kind, outSc.Kind,
			"scene[%d].Kind must be preserved byte-identical (identifier-bearing field, never translated)", i)

		// #6 + #7: scene.Bindings.Clip.ClipID / DriveLink byte-equivalent.
		require.NotNil(t, sc.Bindings.Clip,
			"input scene[%d] must have Clip binding (fixture invariant)", i)
		require.NotNil(t, outSc.Bindings.Clip,
			"output scene[%d] must have Clip binding (preservation invariant)", i)
		assert.Equal(t, sc.Bindings.Clip.ClipID, outSc.Bindings.Clip.ClipID,
			"scene[%d].Bindings.Clip.ClipID must be preserved byte-identical (identifier-bearing, never translated)", i)
		assert.Equal(t, sc.Bindings.Clip.DriveLink, outSc.Bindings.Clip.DriveLink,
			"scene[%d].Bindings.Clip.DriveLink must be preserved byte-identical (identifier-bearing, never translated)", i)

		// text-delta: scene.Text differs from source (translation
		// actually happened). User spec literal: "scene.Text almeno
		// 1 diverso" (at least 1 different) — we use >=1 to match the
		// spec; the stronger all-3-translated invariant is covered by
		// the per-text call-count assertion below (10 translator
		// calls = 1 model.Text + 3 scene.Text + 3 scene.Title + 3
		// image.Prompt proves every text segment was sent through).
		assert.NotEqual(t, sc.Text, outSc.Text,
			"scene[%d].Text must differ from source (text is the canonical translatable field)", i)
		assert.Contains(t, outSc.Text, "IT: ",
			"scene[%d].Text must be prefixed with 'IT: ' (translator byte-prefix)", i)
	}

	// At-least-1 invariant (per user spec literal). The per-text
	// call-count assertion below proves the stronger "all 10
	// segments translated" invariant — this is a redundant
	// regression guard at the user-spec level.
	scenesWithTranslatedText := 0
	for i, sc := range in.SpecScene.Scenes {
		if sc.Text != out.SpecScene.Scenes[i].Text {
			scenesWithTranslatedText++
		}
	}
	assert.GreaterOrEqual(t, scenesWithTranslatedText, 1,
		"at least 1 scene.Text must be translated (user-spec literal 'scene.Text almeno 1 diverso')")

	// ── Provenance fields preserved byte-identical ──
	assert.Equal(t, in.SchemaVersion, out.SchemaVersion,
		"SchemaVersion must be preserved byte-identical")
	assert.Equal(t, in.WordCount, out.WordCount,
		"WordCount must be preserved byte-identical")
	assert.Equal(t, in.ModelUsed, out.ModelUsed,
		"ModelUsed must be preserved byte-identical")
	assert.Equal(t, in.CacheStatus, out.CacheStatus,
		"CacheStatus must be preserved byte-identical")
	assert.Equal(t, in.SpecScene.Version, out.SpecScene.Version,
		"SpecScene.Version must be preserved byte-identical")

	// ── model.Text translated end-to-end ──
	assert.NotEqual(t, in.Text, out.Text,
		"model.Text must differ from source (translated)")
	assert.Contains(t, out.Text, "IT: ",
		"model.Text must be prefixed with 'IT: ' (translator byte-prefix)")

	// ── Per-text call count invariant ──
	// The canonical per-text strategy in TranslateScriptSpec calls
	// the translator exactly once per text segment:
	//   1 model.Text + 3 scene.Text + 3 scene.Title + 3 image.Prompt = 10.
	// This proves the structural-prevention contract: identifier-bearing
	// fields (clip_id, drive_link, image_id, image.url, start_ms,
	// end_ms, scene.id, scene.index, scene.kind) are NEVER sent to
	// the translator.
	assert.Equal(t, 10, tr.calls,
		"translator must be called exactly 10 times (1 model.Text + 3 scene.Text + 3 scene.Title + 3 image.Prompt) — identifier-bearing fields must NEVER be sent to the translator")

	// ── No equal-to-source warnings ──
	// The byte-prefix translator never returns text byte-identical to
	// source (every segment gains the "IT: " prefix), so the canonical
	// WarnTranslationEqualToSource warning should never fire.
	for _, w := range warnings {
		assert.NotContains(t, w, usecase.WarnTranslationEqualToSource,
			"no equal-to-source warning expected on happy-path prefix-translation (translator always returns input + 'IT: ' prefix)")
	}
}

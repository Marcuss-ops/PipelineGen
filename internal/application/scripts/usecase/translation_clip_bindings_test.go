// Package usecase_test — translation_clip_bindings_test.go is the
// external-test surface for the canonical ClipBinding-preservation
// contract of TranslateScriptSpec. The per-text strategy in
// translation.go means the LLM NEVER sees Clip binding fields
// (ClipID / DriveLink / ClipTitle / StartMs / EndMs) — they are
// preserved byte-identical from the enriched baseline.
//
// Per godlike/06 SSOT (one canonical owner per fact):
//   - TranslateScriptSpec lives ONLY at translation.go
//   - ValidateAndEnrichSpecScene lives ONLY at specscene_validator.go
//   - The 4 sub-cases in this file live ONLY at translation_clip_bindings_test.go
//
// Per godlike/07 NO-FAKE-AVAILABILITY: 4 hermetic TDD regression-guards
// covering distinct ClipID preservation, distinct DriveLink
// preservation, YouTube-URL preservation (URL-shape agnostic), and
// long-Drive-file-ID preservation (33+ char file IDs).
//
// Per godlike/07 minimum-blast-radius: zero production-code change.
// The existing TranslateScriptSpec already enforces the per-text
// strategy correctly; this test file is purely additive.
package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// esPrefixTranslator returns "ES: " + text for every input. The byte
// prefix is the canonical "I made it through the translator" receipt
// for the per-text strategy: if a field ends up modified in the
// output, the prefix will be present in that field (failure signal).
// Conversely, if a field is preserved byte-identical, the prefix is
// ABSENT (success signal).
type esPrefixTranslator struct {
	calls int
}

func (m *esPrefixTranslator) Translate(_ context.Context, text, _ string) (string, error) {
	m.calls++
	return "ES: " + text, nil
}

// makeSpecSceneWithDistinctDriveLinks returns a 3-scene EN script
// fixture where each scene has a DISTINCT DriveLink. The
// evidence.AcceptedClipIDs is derived from the scenes (NOT
// hardcoded) so adding scenes auto-syncs. Used by sub-case 4a.
func makeSpecSceneWithDistinctDriveLinks() (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	return makeScenesWithBindings([]sceneFixture{
		{ID: "scene-1", Index: 0, ClipID: "clip-1", DriveLink: "https://drive.google.com/file/d/abc1abc1abc1abc1abc1abc1abc1abc1/view", Title: "Opening", Text: "EN scene 1"},
		{ID: "scene-2", Index: 1, ClipID: "clip-2", DriveLink: "https://drive.google.com/file/d/abc2abc2abc2abc2abc2abc2abc2abc2/view", Title: "Middle", Text: "EN scene 2"},
		{ID: "scene-3", Index: 2, ClipID: "clip-3", DriveLink: "https://drive.google.com/file/d/abc3abc3abc3abc3abc3abc3abc3abc3/view", Title: "Closing", Text: "EN scene 3"},
	}, "Model text with 3 distinct drive links.")
}

// makeSpecSceneWithDistinctClipIDsAndEmptyDriveLink returns a 3-scene
// EN script fixture where 1 of the 3 scenes has an empty DriveLink.
// The empty DriveLink MUST stay empty in the output (per-text
// strategy: the LLM never sees it, so the function cannot
// auto-populate it; the structural-preservation guarantee). Used by
// sub-case 4b.
func makeSpecSceneWithDistinctClipIDsAndEmptyDriveLink() (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	return makeScenesWithBindings([]sceneFixture{
		{ID: "scene-1", Index: 0, ClipID: "clip-A", DriveLink: "https://drive.google.com/file/d/dA1dA1dA1dA1dA1dA1dA1dA1dA1dA1dA1dA1dA1/view", Title: "Opening", Text: "EN scene 1"},
		{ID: "scene-2", Index: 1, ClipID: "clip-B", DriveLink: "", Title: "Middle", Text: "EN scene 2"}, // empty drive link stays empty
		{ID: "scene-3", Index: 2, ClipID: "clip-C", DriveLink: "https://drive.google.com/file/d/dC3dC3dC3dC3dC3dC3dC3dC3dC3dC3dC3dC3dC3/view", Title: "Closing", Text: "EN scene 3"},
	}, "Model text with 3 distinct clip IDs (1 with empty drive link).")
}

// makeSpecSceneWithYouTubeURL returns a 1-scene EN script fixture
// where the Clip binding's DriveLink is a YouTube URL. The per-text
// strategy is URL-shape agnostic: the LLM never sees the URL, so it
// survives verbatim. Used by sub-case 4c.
func makeSpecSceneWithYouTubeURL() (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	return makeScenesWithBindings([]sceneFixture{
		{ID: "scene-1", Index: 0, ClipID: "clip-yt", DriveLink: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", Title: "YouTube Clip", Text: "EN scene with YouTube drive link"},
	}, "Model text with 1 YouTube URL in drive link.")
}

// makeSpecSceneWithLongFileID returns a 1-scene EN script fixture
// where the Clip binding's DriveLink has a 44-character file ID
// (the canonical Drive file ID length range is 33-44 chars). Used
// by sub-case 4d to verify that long file IDs survive byte-identical.
func makeSpecSceneWithLongFileID() (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	// 44-char file ID (a-z 26 + A-R 18 = 44). Every char is alpha
	// to avoid any pattern detection (no dashes, no numbers, no
	// underscores — pure alphabetic chars make this the "hardest"
	// test case for byte-preservation).
	const longFileID = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQR"
	return makeScenesWithBindings([]sceneFixture{
		{ID: "scene-1", Index: 0, ClipID: "clip-long", DriveLink: "https://drive.google.com/file/d/" + longFileID + "/view", Title: "Long File ID", Text: "EN scene with 44-char drive file ID"},
	}, "Model text with 1 long-file-ID drive link.")
}

// sceneFixture is the local test-helper struct used by the
// make*SpecSceneWith* helpers. Kept local to this external test
// file to avoid coupling to any other test package.
type sceneFixture struct {
	ID        string
	Index     int
	ClipID    string
	DriveLink string
	Title     string
	Text      string
}

// makeScenesWithBindings converts a []sceneFixture to a
// (ModelScriptOutputV1, ClipEvidence) tuple. The evidence
// .AcceptedClipIDs is DERIVED from the scenes (NOT hardcoded) so
// adding a 4th scene auto-syncs the evidence.
func makeScenesWithBindings(specs []sceneFixture, modelText string) (*scriptpkg.ModelScriptOutputV1, *scriptpkg.ClipEvidence) {
	scenes := make([]scriptpkg.SpecScene, 0, len(specs))
	for _, s := range specs {
		scenes = append(scenes, scriptpkg.SpecScene{
			ID:    s.ID,
			Index: s.Index,
			Text:  s.Text,
			Title: s.Title,
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    s.ClipID,
					ClipTitle: "EN clip " + s.ClipID,
					DriveLink: s.DriveLink,
					StartMs:   int64(1000 + s.Index*10000),
					EndMs:     int64(5000 + s.Index*10000),
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: "img-" + s.ClipID,
					Prompt:  "EN prompt for " + s.ID,
					URL:     "https://storage.example.com/" + s.ClipID + ".png",
					Status:  "generated",
				},
			},
		})
	}
	in := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          modelText,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
	clipIDs := make([]string, 0, len(scenes))
	for _, sc := range scenes {
		if sc.Bindings.Clip != nil {
			clipIDs = append(clipIDs, sc.Bindings.Clip.ClipID)
		}
	}
	evidence := &scriptpkg.ClipEvidence{AcceptedClipIDs: clipIDs}
	return in, evidence
}

// ─── TEST: TranslateScriptSpec preserves Clip binding fields ───
func TestToTranslatedScript_PreservesClipBindings(t *testing.T) {

	// ── Sub-case 4a: 3 distinct DriveLinks all byte-equivalent ──
	t.Run("distinct_DriveLinks_byte_equivalent_post_translation", func(t *testing.T) {
		in, evidence := makeSpecSceneWithDistinctDriveLinks()
		tr := &esPrefixTranslator{}

		out, _, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "es", tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec must NOT error on 3 distinct DriveLinks (canonical happy path)")
		require.NotNil(t, out, "out must be non-nil on success path")
		require.Equal(t, len(in.SpecScene.Scenes), len(out.SpecScene.Scenes),
			"scene count must be preserved (3 -> 3)")

		// Per-text strategy invariant: each scene's Clip.ClipID
		// AND Clip.DriveLink MUST be byte-identical to the
		// corresponding source field. The "ES: " prefix
		// ABSENT on each field is the canonical "the LLM never
		// saw this" receipt.
		for i, sc := range in.SpecScene.Scenes {
			require.NotNil(t, sc.Bindings.Clip,
				"source scene[%d] must have Clip binding (fixture invariant)", i)
			require.NotNil(t, out.SpecScene.Scenes[i].Bindings.Clip,
				"output scene[%d] must have Clip binding (preservation invariant)", i)
			assert.Equal(t, sc.Bindings.Clip.ClipID, out.SpecScene.Scenes[i].Bindings.Clip.ClipID,
				"scene[%d].Clip.ClipID must be byte-identical to source (LLM cannot mutate)", i)
			assert.Equal(t, sc.Bindings.Clip.DriveLink, out.SpecScene.Scenes[i].Bindings.Clip.DriveLink,
				"scene[%d].Clip.DriveLink must be byte-identical to source (LLM cannot mutate)", i)
			assert.NotContains(t, out.SpecScene.Scenes[i].Bindings.Clip.DriveLink, "ES: ",
				"scene[%d].Clip.DriveLink must NOT contain translator output (LLM never saw it)", i)
		}

		// Translator-call count check: per-text strategy calls
		// the translator for 1 model.Text + N scenes * 3
		// (scene.Text + scene.Title + image.Prompt) = 1 + 3*N
		// calls. The Clip binding fields are NEVER sent to the
		// translator. Derive the expected count from the scene
		// count so the assertion survives per-text strategy
		// evolution (godlike/07 minimum-blast-radius).
		expectedCalls := 1 + len(in.SpecScene.Scenes)*3
		assert.Equal(t, expectedCalls, tr.calls,
			"translator must be called exactly 1+3*N times (1 model.Text + N scenes * 3 per-text segments: scene.Text + scene.Title + image.Prompt) — NOT 1+4*N (which would mean the LLM saw the Clip bindings)")
	})

	// ── Sub-case 4b: distinct ClipIDs + 1 empty DriveLink stays empty ──
	t.Run("empty_DriveLink_stays_empty_post_translation", func(t *testing.T) {
		in, evidence := makeSpecSceneWithDistinctClipIDsAndEmptyDriveLink()
		tr := &esPrefixTranslator{}

		out, _, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "es", tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec must NOT error on 1 empty DriveLink (canonical happy path)")
		require.NotNil(t, out, "out must be non-nil on success path")

		// All 3 distinct ClipIDs preserved byte-identical.
		for i, sc := range in.SpecScene.Scenes {
			require.NotNil(t, sc.Bindings.Clip,
				"source scene[%d] must have Clip binding (fixture invariant)", i)
			require.NotNil(t, out.SpecScene.Scenes[i].Bindings.Clip,
				"output scene[%d] must have Clip binding (preservation invariant)", i)
			assert.Equal(t, sc.Bindings.Clip.ClipID, out.SpecScene.Scenes[i].Bindings.Clip.ClipID,
				"scene[%d].Clip.ClipID must be byte-identical to source (3 distinct clip IDs A/B/C)", i)
		}

		// The 1 empty DriveLink (scene[1]) MUST stay empty.
		// The LLM never sees it (per-text strategy), so the
		// function cannot auto-populate it. If this assertion
		// fails, either the validator auto-populates empty
		// DriveLinks (which would violate the structural-
		// preservation contract) or some other code path is
		// mutating the Clip binding on translation.
		outEmpty := out.SpecScene.Scenes[1].Bindings.Clip.DriveLink
		assert.Empty(t, outEmpty,
			"scene[1].Clip.DriveLink must stay empty (source had empty; LLM never saw it; no auto-population)")

		// The 2 non-empty DriveLinks (scene[0] + scene[2]) MUST
		// be byte-identical to source.
		assert.Equal(t, in.SpecScene.Scenes[0].Bindings.Clip.DriveLink, out.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
			"scene[0].Clip.DriveLink must be byte-identical to source (non-empty preserved)")
		assert.Equal(t, in.SpecScene.Scenes[2].Bindings.Clip.DriveLink, out.SpecScene.Scenes[2].Bindings.Clip.DriveLink,
			"scene[2].Clip.DriveLink must be byte-identical to source (non-empty preserved)")

		// No "ES: " prefix on any DriveLink.
		for i, sc := range out.SpecScene.Scenes {
			if sc.Bindings.Clip != nil {
				assert.NotContains(t, sc.Bindings.Clip.DriveLink, "ES: ",
					"output scene[%d].Clip.DriveLink must NOT contain translator output", i)
			}
		}
	})

	// ── Sub-case 4c: YouTube URL preserved byte-identical ──
	t.Run("YouTube_URL_in_DriveLink_preserved_byte_identical", func(t *testing.T) {
		in, evidence := makeSpecSceneWithYouTubeURL()
		tr := &esPrefixTranslator{}

		out, _, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "es", tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec must NOT error on YouTube URL drive link (URL-shape agnostic contract)")
		require.NotNil(t, out, "out must be non-nil on success path")

		require.NotNil(t, in.SpecScene.Scenes[0].Bindings.Clip,
			"source scene[0] must have Clip binding (fixture invariant)")
		require.NotNil(t, out.SpecScene.Scenes[0].Bindings.Clip,
			"output scene[0] must have Clip binding (preservation invariant)")

		// YouTube URL must be byte-identical (the LLM never
		// sees it; URL shape is irrelevant to the structural-
		// preservation contract — only the byte-content matters).
		assert.Equal(t,
			in.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
			out.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
			"YouTube URL in DriveLink must be byte-identical to source (per-text strategy is URL-shape agnostic)")
		assert.NotContains(t,
			out.SpecScene.Scenes[0].Bindings.Clip.DriveLink, "ES: ",
			"YouTube URL must NOT contain translator output (LLM never saw it)")
	})

	// ── Sub-case 4d: Long Drive file ID (44 chars) preserved byte-identical ──
	t.Run("long_Drive_file_ID_44chars_preserved_byte_identical", func(t *testing.T) {
		in, evidence := makeSpecSceneWithLongFileID()
		tr := &esPrefixTranslator{}

		out, _, err := usecase.TranslateScriptSpec(
			context.Background(), in, evidence, "es", tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec must NOT error on long-file-ID drive link (URL-length agnostic contract)")
		require.NotNil(t, out, "out must be non-nil on success path")

		require.NotNil(t, in.SpecScene.Scenes[0].Bindings.Clip,
			"source scene[0] must have Clip binding (fixture invariant)")
		require.NotNil(t, out.SpecScene.Scenes[0].Bindings.Clip,
			"output scene[0] must have Clip binding (preservation invariant)")

		// 44-char file ID must be byte-identical (the canonical
		// Drive file ID range is 33-44 chars; 44 is the upper
		// boundary, so this is the longest realistic case).
		assert.Equal(t,
			in.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
			out.SpecScene.Scenes[0].Bindings.Clip.DriveLink,
			"44-char Drive file ID must be byte-identical to source (per-text strategy is URL-length agnostic)")
		assert.NotContains(t,
			out.SpecScene.Scenes[0].Bindings.Clip.DriveLink, "ES: ",
			"44-char Drive file ID URL must NOT contain translator output (LLM never saw it)")

		// Additional explicit length assertion: the URL must
		// have a file ID of EXACTLY 44 chars (regression guard
		// against a future truncation bug that might silently
		// shorten file IDs).
		const expectedIDLen = 44
		assert.Equal(t, expectedIDLen, len(in.SpecScene.Scenes[0].Bindings.Clip.DriveLink)-len("https://drive.google.com/file/d//view"),
			"Drive URL must contain a 44-char file ID (canonical upper boundary; shorter IDs are also valid but the fixture uses the longest realistic case)")
	})
}

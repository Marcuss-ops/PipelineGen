// multilingual_persistence_p1f_multilingual_8scenes_test.go
//
// Group 1 - Cross-language consistency (TranslateScriptSpec): 4 tests.
// Extracted atomically from multilingual_persistence_p1f_test.go (P1F, 2026-07-04).
// Uses helpers + p1fSemanticMarkersPerScene map from the companion
// multilingual_persistence_p1f_helpers_test.go file (same usecase package).
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the 4
// TestMultilingual_8Scenes_4Languages_* test functions. Other groups
// (CanonicalCase, MissingTranslation, AuditTrail) remain in the
// original file pending subsequent atomic-extract commits.

package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestMultilingual_8Scenes_4Languages_StructurePreserved(t *testing.T) {
	t.Parallel()

	in := makeEightScenePacquiaoSpecEN()
	evidence := evidenceFor8Scenes()
	languages := []string{"it", "en", "es", "pt"}

	// Per-language results + a canonical structural-fingerprint
	// derived from the output. The fingerprint includes:
	//   - scene count
	//   - scene IDs (positional, by Index)
	//   - scene kinds (positional, by Index)
	//   - clip_id per scene (positional)
	//   - drive_link per scene
	//   - image_id per scene
	//   - Index per scene
	//   - StartMs + EndMs per scene (the timestamp
	//     preservation invariant — "no narrative loss" must
	//     include the time-coordinates)
	// Text fields are NOT included in the fingerprint (per
	// user spec "NON confrontare parola per parola").
	type fingerprint struct {
		sceneCount  int
		sceneIDs    []string
		sceneKinds  []scriptpkg.SceneKind
		clipIDs     []string
		driveLinks  []string
		imageIDs    []string
		indexes     []int
		startMs     []int64
		endMs       []int64
		specVersion int
	}

	mkFingerprint := func(out *scriptpkg.ModelScriptOutputV1) fingerprint {
		fp := fingerprint{
			sceneCount:  len(out.SpecScene.Scenes),
			sceneIDs:    make([]string, len(out.SpecScene.Scenes)),
			sceneKinds:  make([]scriptpkg.SceneKind, len(out.SpecScene.Scenes)),
			clipIDs:     make([]string, len(out.SpecScene.Scenes)),
			driveLinks:  make([]string, len(out.SpecScene.Scenes)),
			imageIDs:    make([]string, len(out.SpecScene.Scenes)),
			indexes:     make([]int, len(out.SpecScene.Scenes)),
			startMs:     make([]int64, len(out.SpecScene.Scenes)),
			endMs:       make([]int64, len(out.SpecScene.Scenes)),
			specVersion: out.SpecScene.Version,
		}
		for i, sc := range out.SpecScene.Scenes {
			fp.sceneIDs[i] = sc.ID
			fp.sceneKinds[i] = sc.Kind
			fp.indexes[i] = sc.Index
			if sc.Bindings.Clip != nil {
				fp.clipIDs[i] = sc.Bindings.Clip.ClipID
				fp.driveLinks[i] = sc.Bindings.Clip.DriveLink
				fp.startMs[i] = sc.Bindings.Clip.StartMs
				fp.endMs[i] = sc.Bindings.Clip.EndMs
			}
			if sc.Bindings.Image != nil {
				fp.imageIDs[i] = sc.Bindings.Image.ImageID
			}
		}
		return fp
	}

	// Run all 4 languages and collect fingerprints. The translator
	// appends a per-language suffix; the test asserts structural
	// equivalence across all 4 outputs.
	fingerprints := make(map[string]fingerprint)
	for _, lang := range languages {
		tr := &mockTranslatorSuffix{suffix: strings.ToUpper(lang)}
		out, warnings, err := TranslateScriptSpec(
			context.Background(), in, evidence, lang, tr.Translate)
		require.NoError(t, err,
			"TranslateScriptSpec at %s MUST succeed on valid input", lang)
		require.NotNil(t, out, "out MUST be non-nil for %s", lang)
		require.NotNil(t, warnings, "warnings MUST be non-nil for %s", lang)

		fingerprints[lang] = mkFingerprint(out)
	}

	// User-spec invariant 1: scene count = 8 for every language.
	for lang, fp := range fingerprints {
		assert.Equal(t, 8, fp.sceneCount,
			"%s output MUST have 8 scenes (user spec: stessa copertura eventi). got=%d",
			lang, fp.sceneCount)
	}

	// User-spec invariant 2: scene IDs identical across all 4
	// languages (the canonical ordering: scene-1..scene-8).
	reference := fingerprints["it"]
	for lang, fp := range fingerprints {
		assert.Equal(t, reference.sceneIDs, fp.sceneIDs,
			"%s scene IDs MUST match the it reference (same coverage). got=%v",
			lang, fp.sceneIDs)
		assert.Equal(t, reference.sceneKinds, fp.sceneKinds,
			"%s scene kinds MUST match the it reference. got=%v",
			lang, fp.sceneKinds)
		assert.Equal(t, reference.indexes, fp.indexes,
			"%s scene Indexes MUST match the it reference (same order). got=%v",
			lang, fp.indexes)
		assert.Equal(t, reference.specVersion, fp.specVersion,
			"%s SpecScene.Version MUST match the it reference. got=%d",
			lang, fp.specVersion)
		// Timestamps preserved across all 4 languages (the
		// "nessuna perdita narrativa" invariant must include
		// the time-coordinates — a regression that dropped
		// StartMs/EndMs would silently break the video
		// cut range).
		assert.Equal(t, reference.startMs, fp.startMs,
			"%s StartMs MUST match the it reference (timestamp preserved). got=%v",
			lang, fp.startMs)
		assert.Equal(t, reference.endMs, fp.endMs,
			"%s EndMs MUST match the it reference (timestamp preserved). got=%v",
			lang, fp.endMs)
	}

	// User-spec invariant 3: clip_id + drive_link + image_id
	// byte-identical across languages (the per-text strategy
	// never exposes identifiers to the translator).
	for lang, fp := range fingerprints {
		assert.Equal(t, reference.clipIDs, fp.clipIDs,
			"%s clip_ids MUST match the it reference (no clip loss). got=%v",
			lang, fp.clipIDs)
		assert.Equal(t, reference.driveLinks, fp.driveLinks,
			"%s drive_links MUST match the it reference (binding preserved). got=%v",
			lang, fp.driveLinks)
		assert.Equal(t, reference.imageIDs, fp.imageIDs,
			"%s image_ids MUST match the it reference (binding preserved). got=%v",
			lang, fp.imageIDs)
	}

	// User-spec invariant 4: "nessuna perdita narrativa" — every
	// Pacquiao-Broner clip is bound in every language's output
	// (the 8 clips must all appear, none lost).
	requiredClips := []string{
		"clip-r1", "clip-r2", "clip-r5", "clip-r7",
		"clip-r9", "clip-r10", "clip-r12", "clip-post",
	}
	for lang, fp := range fingerprints {
		bound := make(map[string]struct{})
		for _, cid := range fp.clipIDs {
			bound[cid] = struct{}{}
		}
		for _, req := range requiredClips {
			_, ok := bound[req]
			assert.True(t, ok,
				"%s MUST have clip %q bound (no narrative loss). got clip_ids=%v",
				lang, req, fp.clipIDs)
		}
	}

	// User-spec invariant 5: "nessun cambio di significato" —
	// semantic markers (entity names + round numbers) MUST be
	// preserved in the translated text. The translator's
	// suffix-translation pattern preserves the entire source
	// text + appends a suffix, so every source marker is in
	// the output. This is the load-bearing assertion for
	// semantic equivalence.
	//
	// The check is PER-SCENE (not per-concatenated-text) so a
	// regression that drops a marker from a single scene is
	// caught even if the same marker appears in another scene.
	for _, lang := range languages {
		tr := &mockTranslatorSuffix{suffix: strings.ToUpper(lang)}
		out, _, err := TranslateScriptSpec(
			context.Background(), in, evidence, lang, tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec at %s MUST succeed", lang)
		require.NotNil(t, out)

		// Per-scene semantic-marker probe.
		// PRE-EXISTING-7 / FASE 13 PART 2: p1fSemanticMarkersPerScene
		// map keys are 1-based (human-readable); convert to 0-based
		// for slice access. The legacy require.Greater(t,len,sceneIdx)
		// failed at sceneIdx=8 because len=8 and the slice index 8 was
		// out of bounds.
		for sceneIdx, markers := range p1fSemanticMarkersPerScene {
			scIdx := sceneIdx - 1
			require.Greater(t, len(out.SpecScene.Scenes), scIdx,
				"%s output MUST have at least %d scenes for the semantic-marker probe", lang, sceneIdx)
			sc := out.SpecScene.Scenes[scIdx]
			// Each per-scene marker must appear in either the
			// scene's Text or Title (the canonical translatable
			// fields).
			sceneText := sc.Text + " " + sc.Title
			for _, marker := range markers {
				assert.Contains(t, sceneText, marker,
					"%s scene[%d] (id=%q) MUST preserve semantic marker %q (no meaning change). scene text=%q",
					lang, sceneIdx, sc.ID, marker, sceneText)
			}
		}

		// Per-language suffix appended to at least one scene's
		// text (confirms the translator was actually called
		// for this language).
		suffixSeen := false
		for _, sc := range out.SpecScene.Scenes {
			if strings.Contains(sc.Text, p1fPerLanguageMarkers(lang)) {
				suffixSeen = true
				break
			}
		}
		assert.True(t, suffixSeen,
			"%s MUST have the per-language suffix appended in at least one scene (translator called). lang=%s",
			lang, p1fPerLanguageMarkers(lang))
	}
}

// TestMultilingual_8Scenes_4Languages_OrderPreserved pins the
// user-spec invariant: "stesso ordine" — input order is
// preserved in the output. scene[0].ID = "scene-1" must be
// scene[0].ID in every language. The validator must NOT
// reorder scenes across languages.
//
// This is a focused companion to
// TestMultilingual_8Scenes_4Languages_StructurePreserved
// (which checks IDs as a set); here we check IDs as a
// SEQUENCE.
func TestMultilingual_8Scenes_4Languages_OrderPreserved(t *testing.T) {
	t.Parallel()

	in := makeEightScenePacquiaoSpecEN()
	evidence := evidenceFor8Scenes()
	languages := []string{"it", "en", "es", "pt"}

	// Expected order (positional, not set).
	expectedOrder := []string{
		"scene-1", "scene-2", "scene-3", "scene-4",
		"scene-5", "scene-6", "scene-7", "scene-8",
	}

	for _, lang := range languages {
		tr := &mockTranslatorSuffix{suffix: strings.ToUpper(lang)}
		out, _, err := TranslateScriptSpec(
			context.Background(), in, evidence, lang, tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec at %s MUST succeed", lang)
		require.NotNil(t, out)
		require.Len(t, out.SpecScene.Scenes, 8,
			"%s output MUST have 8 scenes (order assertion baseline)", lang)

		// Positional equality: scene[0].ID == "scene-1" etc.
		for i, sc := range out.SpecScene.Scenes {
			assert.Equal(t, expectedOrder[i], sc.ID,
				"%s scene[%d].ID = %q, want %q (order preserved invariant)",
				lang, i, sc.ID, expectedOrder[i])
			assert.Equal(t, i, sc.Index,
				"%s scene[%d].Index = %d (Index MUST match array position)",
				lang, i, sc.Index)
		}
	}
}

// TestMultilingual_8Scenes_4Languages_NoSceneLossNoTruncation pins
// the user-spec invariant: "nessuna perdita narrativa" — every
// scene has non-empty text in every language (no truncation, no
// silent scene loss). This is the structural-coverage floor.
func TestMultilingual_8Scenes_4Languages_NoSceneLossNoTruncation(t *testing.T) {
	t.Parallel()

	in := makeEightScenePacquiaoSpecEN()
	evidence := evidenceFor8Scenes()
	languages := []string{"it", "en", "es", "pt"}

	for _, lang := range languages {
		tr := &mockTranslatorSuffix{suffix: strings.ToUpper(lang)}
		out, _, err := TranslateScriptSpec(
			context.Background(), in, evidence, lang, tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec at %s MUST succeed", lang)
		require.NotNil(t, out)
		require.Len(t, out.SpecScene.Scenes, 8,
			"%s output MUST have 8 scenes (no scene loss)", lang)

		// Every scene's Text + Title non-empty (no truncation).
		for i, sc := range out.SpecScene.Scenes {
			assert.NotEmpty(t, strings.TrimSpace(sc.Text),
				"%s scene[%d].Text MUST be non-empty (no truncation). got=%q",
				lang, i, sc.Text)
			assert.NotEmpty(t, strings.TrimSpace(sc.Title),
				"%s scene[%d].Title MUST be non-empty (no truncation). got=%q",
				lang, i, sc.Title)
		}

		// Top-level Text non-empty.
		assert.NotEmpty(t, strings.TrimSpace(out.Text),
			"%s top-level Text MUST be non-empty (no truncation)", lang)

		// The 8th (last) scene has non-empty text (no tail truncation).
		last := out.SpecScene.Scenes[7]
		assert.NotEmpty(t, strings.TrimSpace(last.Text),
			"%s last scene (scene-8) MUST have non-empty text (no tail truncation). got=%q",
			lang, last.Text)
	}
}

// TestMultilingual_8Scenes_4Languages_TranslatorCallCount pins
// the canonical per-text strategy: the translator is called
// exactly N times per language, where N = 1 (top-level Text)
// + 8 (scene.Text) + 8 (scene.Title) + 8 (scene.Bindings.Image.Prompt)
// = 25 calls.
//
// Same N for every language (the input structure is identical;
// the translator is a pure function of the text + target
// language). This is the canonical pin for the "no duplicate
// / no missing calls" invariant across the 4-language
// envelope.
func TestMultilingual_8Scenes_4Languages_TranslatorCallCount(t *testing.T) {
	t.Parallel()

	in := makeEightScenePacquiaoSpecEN()
	evidence := evidenceFor8Scenes()
	languages := []string{"it", "en", "es", "pt"}

	// 1 top-level Text + 8 scene.Text + 8 scene.Title + 8 image.Prompt = 25.
	const expectedCalls = 25

	for _, lang := range languages {
		tr := &mockTranslatorSuffix{suffix: strings.ToUpper(lang)}
		_, _, err := TranslateScriptSpec(
			context.Background(), in, evidence, lang, tr.Translate)
		require.NoError(t, err, "TranslateScriptSpec at %s MUST succeed", lang)
		assert.Equal(t, expectedCalls, tr.calls,
			"%s translator MUST be called exactly %d times (1 top + 8 scene.Text + 8 scene.Title + 8 image.Prompt). got=%d",
			lang, expectedCalls, tr.calls)
	}
}

// ── Group 2 helpers: TextTrackResolver test seam ───────────────────────

// p1fStubRepo is the canonical TextTrackRepository stub used
// by the Group 2 + Group 3 tests. The production stubRepo
// (in text_track_resolver_test.go, package usecase_test) is
// NOT directly importable here, so we define a local stub

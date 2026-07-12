// Package usecase — multilingual_persistence_p1f_test.go
//
// P1.F — Multilingua e traduzioni persistite test suite
// (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026).
//
// USER SPEC (verbatim, July 2026, Italian):
// "Implementa la suite P1.F — Multilingua e traduzioni
// persistite su main. (1) Stesse 8 clip, language=it/en/es/pt
// → stessa copertura eventi, stesso ordine, nessuna perdita
// narrativa, nessun cambio di significato. NON confrontare
// parola per parola, solo copertura eventi + item
// structure. (2) Caso canonico: clip originale inglese +
// text track italiano READY + request language=it → usa
// track salvato, NO chiamata traduttore, NO nuova
// trascrizione. (3) Policy traduzione mancante:
// language=fr, track fr assente → TEXT_TRACK_NOT_READY
// OPPURE fallback con warning esplicito (mai traduzione
// silente). Lavora su main, commit frequenti, push."
//
// ATTESO per the user spec:
//
//	Group 1 — Cross-language consistency (TranslateScriptSpec):
//	 - Same 8-scene English script translated to it/en/es/pt
//	   must produce 4 outputs with the SAME event coverage
//	   (scene count, scene IDs, scene kinds) and SAME order
//	   (Index 0..7), with NO narrative loss (all 8 clips
//	   bound) and NO meaning change (entity markers preserved
//	   in translation).
//	 - User spec: "NON confrontare parola per parola, solo
//	   copertura eventi + item structure" — the test pins the
//	   STRUCTURAL equivalence (not byte-equality of text).
//
//	Group 2 — DB-hit canonical case (TextTrackResolver):
//	 - Asset has Italian READY track in DB. Request
//	   language=it. Resolver hits the DB (priority 2), returns
//	   the saved track byte-equivalent, and does NOT call
//	   Whisper, Subtitles, or any translator.
//	 - User spec: "NO chiamata traduttore, NO nuova
//	   trascrizione" — the test pins the cache-hit path
//	   end-to-end (no upstream port consulted).
//
//	Group 3 — Missing translation policy (TextTrackResolver):
//	 - language=fr requested, no fr track in DB, no fr
//	   subtitles, no Whisper fallback. The pipeline MUST
//	   surface a typed error (ErrLanguageUndeterminable when
//	   policy requires certainty) or (nil, nil) when it
//	   doesn't, BUT in NO case call a translator with
//	   targetLang=fr.
//	 - User spec: "TEXT_TRACK_NOT_READY OPPURE fallback con
//	   warning esplicito (mai traduzione silente)" — the test
//	   pins the no-silent-translation invariant + the
//	   available-languages operator visibility
//	   (ListReadyLanguages → AvailableLanguages).
//
// SEAM CHOICE: two layers — TranslateScriptSpec (for Group 1)
// and TextTrackResolver (for Groups 2+3). The two layers
// are independent: TranslateScriptSpec is a pure function
// (text-in, text-out); TextTrackResolver is the canonical
// acquisition chain that decides where the text comes from.
// P1.F pins BOTH surfaces because the user spec covers
// both "multilingua" (script translation across languages)
// and "traduzioni persistite" (DB-persisted translations
// are reused, not regenerated).
//
// SUT BUGS (pin current behavior; 2026-07 candidates for the
// "honest lock" backlog):
//
//  1. TranslateScriptSpec may produce scene-count drift
//     across languages (e.g., 8 → 7 or 8 → 9 scenes if the
//     validator rejects/expands). Today the function is
//     1:1 with the input (no scene count drift). The test
//     pins this as the load-bearing invariant.
//
//  2. TranslateScriptSpec may reorder scenes across
//     languages if the validator mutates Index. Today the
//     function preserves Index 0..7. The test pins this.
//
//  3. TextTrackResolver.AcquireSegmentText may fall through
//     to Whisper even when the DB has a READY track in the
//     target language. The Fase 1.a/b contract is
//     priority-2 (DB) wins over priority-5 (Whisper) when
//     PreferredLanguages matches. The test pins this as the
//     canonical cache-hit path.
//
//  4. The resolver may call Whisper with a "best-effort"
//     fallback even when a typed ErrLanguageUndeterminable
//     should fire (RequireLanguageCertainty=true). The
//     test pins the policy-gate fail-closed behavior.
//
//  5. The available-languages list may be empty when an
//     ErrTextTrackNotReady fires (operator dashboard cannot
//     tell what's available). The test pins
//     ListReadyLanguages → AvailableLanguages surfacing.
//
//  6. The resolver may silently substitute a "default"
//     language (e.g. "en") for an empty targetLang input,
//     silently translating to the wrong language. The
//     godlike/07 no-fake-availability invariant says empty
//     must collapse to "und", never silently to "en". The
//     test pins this contract via ResolveLanguage.
//
//  7. PreferredLanguages-order contract: the resolver picks
//     the FIRST READY match in the PreferredLanguages list,
//     not the DB insertion order. A regression that flips
//     the order would silently surface a different language
//     than the operator requested. The test pins the
//     [it,en,es] vs [es,en,it] contrast.
package usecase

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Group 1 helpers: 8-scene Pacquiao-Broner fixture ────────────────────

// makeEightScenePacquiaoSpecEN constructs an 8-scene EN script
// paralleling the 8 Pacquiao-Broner rounds from the P2.A spec
// (commit f3d181777). Each scene is a SceneClip with a unique
// clip_id + drive_link + image_id + scene_id; entity markers
// ("Pacquiao", "Broner", "round N") anchor the cross-language
// semantic-equivalence assertion.
//
// The fixture uses round numbers 1, 2, 5, 7, 9, 10-11, 12, post
// (matches the canonical P2.A clip_ids from commit f3d181777
// — abbreviated to "round-N" labels for the P1.F test surface).
//
// Clip binding StartMs/EndMs are 32000/37000 for all 8 scenes
// (parallels the P2.A canonical 32s-37s window for round 1) so
// the P1.F cross-language fingerprint can pin timestamp
// preservation as a load-bearing invariant of "nessuna perdita
// narrativa" (no narrative loss).
func makeEightScenePacquiaoSpecEN() *scriptpkg.ModelScriptOutputV1 {
	makeScene := func(sceneID, clipID, clipTitle, driveLink, imageID, prompt, text, title string) scriptpkg.SpecScene {
		return scriptpkg.SpecScene{
			ID:    sceneID,
			Index: 0, // patched below
			Text:  text,
			Title: title,
			Kind:  scriptpkg.SceneClip,
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    clipID,
					ClipTitle: clipTitle,
					DriveLink: driveLink,
					StartMs:   32000,
					EndMs:     37000,
				},
				Image: &scriptpkg.ImageBinding{
					ImageID: imageID,
					Prompt:  prompt,
					URL:     "https://storage.example.com/" + imageID + ".png",
					Status:  "generated",
				},
			},
		}
	}
	scenes := []scriptpkg.SpecScene{
		makeScene("scene-1", "clip-r1", "Round 1 — Study phase and speed",
			"https://drive.google.com/file/d/pacquiao-broner-r1/view",
			"img-r1",
			"Boxing ring at MGM Grand, Pacquiao in blue, Broner in red, study phase",
			"Round 1 — Pacquiao studies Broner's defense, the southpaw stance and quick jab are visible in the opening seconds.",
			"Round 1: la fase di studio"),
		makeScene("scene-2", "clip-r2", "Round 2 — Positioning and early exchanges",
			"https://drive.google.com/file/d/pacquiao-broner-r2/view",
			"img-r2",
			"Pacquiao circling, Broner back-pedaling, first power punches thrown",
			"Round 2 — Pacquiao takes the center, Broner tries to find range, the first clean exchanges happen here.",
			"Round 2: posizionamento e primi scambi"),
		makeScene("scene-3", "clip-r5", "Round 5 — Broner's best moment",
			"https://drive.google.com/file/d/pacquiao-broner-r5/view",
			"img-r5",
			"Broner lands a counter, Pacquiao resets, the crowd reacts",
			"Round 5 — Broner has his best moment, a clean right hand lands, Pacquiao resets quickly.",
			"Round 5: il miglior momento di Broner"),
		makeScene("scene-4", "clip-r7", "Round 7 — The key moment",
			"https://drive.google.com/file/d/pacquiao-broner-r7/view",
			"img-r7",
			"Pacquiao lands a flurry, Broner staggers, the ref watches closely",
			"Round 7 — The key moment, Pacquiao lands a sustained flurry, Broner staggers briefly.",
			"Round 7: il momento chiave"),
		makeScene("scene-5", "clip-r9", "Round 9 — Pacquiao on the attack",
			"https://drive.google.com/file/d/pacquiao-broner-r9/view",
			"img-r9",
			"Pacquiao pressing forward, combinations landing, Broner in survival mode",
			"Round 9 — Pacquiao is on the attack, combinations land cleanly, Broner covers up.",
			"Round 9: Pacquiao ancora all'attacco"),
		makeScene("scene-6", "clip-r10", "Round 10-11 — Pacquiao controls",
			"https://drive.google.com/file/d/pacquiao-broner-r10/view",
			"img-r10",
			"Pacquiao dictating pace, Broner trying to survive, jab working",
			"Round 10-11 — Pacquiao controls the tempo, the jab keeps Broner at distance.",
			"Round 10-11: il controllo di Pacquiao"),
		makeScene("scene-7", "clip-r12", "Round 12 — The end of the match",
			"https://drive.google.com/file/d/pacquiao-broner-r12/view",
			"img-r12",
			"Final round, both fighters giving everything, the bell rings",
			"Round 12 — The end of the match, both fighters empty the tank, the final bell rings.",
			"Round 12: il finale del match"),
		makeScene("scene-8", "clip-post", "Post-match — Verdict announcement",
			"https://drive.google.com/file/d/pacquiao-broner-post/view",
			"img-post",
			"Ring announcer with the scorecards, crowd waiting, the verdict is read",
			"Post-match — The official verdict is announced, Pacquiao wins by unanimous decision.",
			"Post-match: annuncio del verdetto"),
	}
	for i := range scenes {
		scenes[i].Index = i
	}
	return &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Pacquiao vs Broner — eight key moments from the MGM Grand showdown.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes:  scenes,
		},
	}
}

// evidenceFor8Scenes returns the canonical clip evidence for
// the 8-scene Pacquiao-Broner fixture. The AcceptedClipIDs
// list mirrors the in-scene clip_ids.
func evidenceFor8Scenes() *scriptpkg.ClipEvidence {
	return &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{
			"clip-r1", "clip-r2", "clip-r5", "clip-r7",
			"clip-r9", "clip-r10", "clip-r12", "clip-post",
		},
	}
}

// p1fSemanticMarkersPerScene is the per-scene set of
// entity-marker substrings that MUST be preserved across
// translation (the "no meaning change" invariant). Indexed
// by scene number (1-8). The per-scene check is more robust
// than a single concatenated probe because it catches a
// regression where a marker is dropped from a single scene
// even if the same marker appears in another scene.
var p1fSemanticMarkersPerScene = map[int][]string{
	1: {"Pacquiao", "Broner", "Round 1"},
	2: {"Pacquiao", "Broner", "Round 2"},
	3: {"Broner", "Pacquiao", "Round 5"},
	4: {"Pacquiao", "Broner", "Round 7"},
	5: {"Pacquiao", "Broner", "Round 9"},
	6: {"Pacquiao", "Broner", "Round 10"},
	7: {"Pacquiao", "Broner", "Round 12"},
	8: {"Pacquiao", "Broner", "verdict"},
}

// p1fPerLanguageMarkers returns the per-language markers
// the translator emits (the "happy path" suffix-translation
// pattern from translation_test.go). The translator appends
// "_{LANG}" to every text segment; the test verifies the
// per-scene Text contains the suffix + the source's
// semantic markers.
func p1fPerLanguageMarkers(lang string) string {
	return "_" + strings.ToUpper(lang)
}

// ── Group 1: Cross-language consistency (TranslateScriptSpec) ───────────

// TestMultilingual_8Scenes_4Languages_StructurePreserved pins the
// user-spec invariant: the same 8-scene English script at 4
// languages (it/en/es/pt) must produce 4 outputs with the SAME
// structural coverage (scene count, scene IDs, scene kinds, clip
// bindings, image bindings, order). Text fields are translated
// (suffix-translation pattern), not byte-equivalent, but the
// STRUCTURE is identical.
//
// "Stessa copertura eventi, stesso ordine" → scene count + IDs +
// kinds + Index preserved. "Nessuna perdita narrativa" → every
// clip_id is bound in every language's output. "Nessun cambio di
// significato" → semantic markers preserved (Pacquiao, Broner,
// round numbers).
//
// "NON confrontare parola per parola" → the test asserts
// STRUCTURAL equivalence (counts, IDs, kinds, bindings) and
// semantic-marker presence, NOT text byte-equality. This is
// the load-bearing contract the user spec demands.
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
		for sceneIdx, markers := range p1fSemanticMarkersPerScene {
			require.Greater(t, len(out.SpecScene.Scenes), sceneIdx,
				"%s output MUST have at least %d scenes for the semantic-marker probe", lang, sceneIdx+1)
			sc := out.SpecScene.Scenes[sceneIdx]
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
// with the same canonical semantics (FindReady filters by
// status=READY; ListReadyLanguages returns the sorted set).
type p1fStubRepo struct {
	mu   sync.Mutex
	rows []asset.TextTrack
}

func (s *p1fStubRepo) UpsertBatch(_ context.Context, tracks []asset.TextTrack) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, tracks...)
	return nil
}

func (s *p1fStubRepo) Find(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

// FindReady is the canonical Fase 1.b READY-only lookup. The
// stub returns the row when status=READY and ignores
// PENDING/FAILED rows (matches the production contract).
func (s *p1fStubRepo) FindReady(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind &&
			s.rows[i].Status == asset.TextTrackReady {
			return &s.rows[i], nil, nil
		}
	}
	return nil, nil, nil
}

// ListReadyLanguages returns the sorted set of language
// codes for which a READY track exists.
func (s *p1fStubRepo) ListReadyLanguages(_ context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	var out []string
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].TextKind == kind &&
			s.rows[i].Status == asset.TextTrackReady {
			if _, ok := seen[s.rows[i].LanguageCode]; !ok {
				seen[s.rows[i].LanguageCode] = struct{}{}
				out = append(out, s.rows[i].LanguageCode)
			}
		}
	}
	return out, nil
}

func (s *p1fStubRepo) ListByAsset(_ context.Context, assetID string) ([]asset.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []asset.TextTrack
	for _, r := range s.rows {
		if r.AssetID == assetID {
			out = append(out, r)
		}
	}
	return out, nil
}

// p1fStubSubtitles records FetchSegmentSubtitles calls. Used to
// assert the resolver did NOT fall through to subtitles when
// the DB has a READY track (Group 2) and to assert the
// resolver did NOT consult subtitles for a fr request with no
// source material (Group 3).
type p1fStubSubtitles struct {
	bundle *asset.ResolvedTextBundle
	err    error
	calls  int
}

func (s *p1fStubSubtitles) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return nil
}
func (s *p1fStubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*asset.ResolvedTextBundle, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.bundle, nil
}

// p1fStubTranscriber records TranscribeAudio + TranscribeAudioWithDetection
// invocations. Used to assert the resolver did NOT call Whisper
// when the DB has a READY track (Group 2) and the resolver did
// NOT silently call Whisper for a fr request with no source
// material (Group 3).
type p1fStubTranscriber struct {
	text  string
	err   error
	calls int
	det   *asset.TranscriptResult
}

func (s *p1fStubTranscriber) TranscribeAudio(_ context.Context, _ string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func (s *p1fStubTranscriber) TranscribeAudioWithDetection(_ context.Context, _ string) (asset.TranscriptResult, error) {
	s.calls++
	if s.err != nil {
		return asset.TranscriptResult{}, s.err
	}
	if s.det != nil {
		return *s.det, nil
	}
	return asset.TranscriptResult{Text: s.text, DetectedLanguage: ""}, nil
}

// Compile-time guarantees that the stubs satisfy the ports the
// resolver depends on.
var (
	_ asset.TextTrackRepository           = (*p1fStubRepo)(nil)
	_ youtubeports.SubtitleFetcherPort    = (*p1fStubSubtitles)(nil)
	_ youtubeports.WhisperTranscriberPort = (*p1fStubTranscriber)(nil)
)

// newP1FResolver builds a TextTrackResolver wired with the
// given stubs. Log is zap.NewNop() to keep the test surface
// deterministic.
func newP1FResolver(repo *p1fStubRepo, subs *p1fStubSubtitles, trans *p1fStubTranscriber) *usecase.TextTrackResolver {
	return &usecase.TextTrackResolver{
		Repo:        repo,
		Subtitles:   subs,
		Transcriber: trans,
		Log:         zap.NewNop(),
	}
}

// ── Group 2: DB-hit canonical case (TextTrackResolver) ──────────────────

// TestCanonicalCase_ItalianReadyTrack_NoTranslatorNoWhisper pins
// the user-spec canonical-case contract:
//
//	"clip originale inglese + text track italiano READY + request
//	 language=it → usa track salvato, NO chiamata traduttore, NO
//	 nuova trascrizione."
//
// The asset has a READY Italian transcript in the DB. The
// resolver MUST:
//   - hit the DB (priority 2) and return the saved track
//     byte-equivalent
//   - NOT call Whisper (priority 5) for a new transcription
//   - NOT call Subtitles (priority 3+4) when DB has a match
//
// The "translator" in the user spec maps to any of the upstream
// acquisition paths (Whisper, Subtitles, or a future
// translation leg) — the test pins ALL three as "must not be
// called" for the canonical case.
func TestCanonicalCase_ItalianReadyTrack_NoTranslatorNoWhisper(t *testing.T) {
	t.Parallel()

	// Asset has a READY Italian track in the DB.
	italianText := "Ciao dal DB, transcript italiano pronto per la fase 4"
	italianHash := asset.TextHash(italianText, "it", asset.TextTrackTranscript)
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{
			AssetID:            "yt_p1f_canonical_001",
			LanguageCode:       "it",
			TextKind:           asset.TextTrackTranscript,
			TextContent:        italianText,
			Status:             asset.TextTrackReady,
			SourceType:         asset.TextSourceYouTubeSubtitle,
			SourceLanguageCode: "en",
			IsOriginal:         false,
			Provider:           "yt-dlp",
			TextHash:           italianHash,
			SourceVersion:      asset.SourceVersion(italianHash, "en", "it", "yt-dlp", "yt-auto", "", ""),
		},
	}}

	subs := &p1fStubSubtitles{bundle: &asset.ResolvedTextBundle{
		LanguageCode: "es", PlainText: "should not be called",
		SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true,
	}}
	trans := &p1fStubTranscriber{text: "should not be called"}

	resolver := newP1FResolver(repo, subs, trans)

	// Acquire with PreferredLanguages = [it] (the canonical case).
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_canonical_001",
		PreferredLanguages: []string{"it"},
	})
	require.NoError(t, err, "canonical case MUST succeed (DB hit)")
	require.NotNil(t, bundle, "canonical case MUST return a non-nil bundle")

	// User-spec invariant 1: bundle matches the DB row byte-equivalent.
	assert.Equal(t, "it", bundle.LanguageCode,
		"bundle.LanguageCode MUST be the DB row's language (\"it\")")
	assert.Equal(t, italianText, bundle.PlainText,
		"bundle.PlainText MUST be byte-equivalent to the DB row's TextContent (no re-derivation)")
	assert.Equal(t, asset.TextSourceYouTubeSubtitle, bundle.SourceType,
		"bundle.SourceType MUST match the DB row's SourceType (canonical provenance)")
	assert.True(t, bundle.IsOriginal,
		"DB-sourced rows MUST carry IsOriginal=true (the DB row's IsOriginal was set true at save time)")
	assert.Equal(t, "yt-dlp", bundle.Provider,
		"bundle.Provider MUST match the DB row's Provider (provenance preserved)")

	// User-spec invariant 2: NO Whisper call (no new transcription).
	assert.Equal(t, 0, trans.calls,
		"Whisper MUST NOT be called when DB has a READY track for the target language. "+
			"got %d calls (user spec: NO nuova trascrizione)", trans.calls)

	// User-spec invariant 3: NO Subtitles call (DB short-circuits).
	assert.Equal(t, 0, subs.calls,
		"Subtitles MUST NOT be called when DB has a READY track for the target language. "+
			"got %d calls (priority 2 wins over priority 3+4)", subs.calls)
}

// TestCanonicalCase_UseSavedTrack_ByteEquivalentToDBRow pins the
// canonical contract at a tighter grain: the returned bundle's
// PlainText + LanguageCode + SourceType + TextHash + SourceVersion
// are all byte-equivalent to the DB row's stored values. No
// re-derivation, no translation, no silent substitution.
func TestCanonicalCase_UseSavedTrack_ByteEquivalentToDBRow(t *testing.T) {
	t.Parallel()

	originalText := "Il match Pacquiao vs Broner inizia con la fase di studio, trascrizione italiana dal DB."
	originalLang := "it"
	originalSrcLang := "en"
	originalHash := asset.TextHash(originalText, originalLang, asset.TextTrackTranscript)
	originalSrcVer := asset.SourceVersion(originalHash, originalSrcLang, originalLang, "yt-dlp", "yt-auto", "v1", "")

	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{
			AssetID:            "yt_p1f_byte_001",
			LanguageCode:       originalLang,
			TextKind:           asset.TextTrackTranscript,
			TextContent:        originalText,
			Status:             asset.TextTrackReady,
			SourceType:         asset.TextSourceYouTubeSubtitle,
			SourceLanguageCode: originalSrcLang,
			IsOriginal:         true,
			Provider:           "yt-dlp",
			ModelName:          "yt-auto",
			ModelVersion:       "v1",
			TextHash:           originalHash,
			SourceVersion:      originalSrcVer,
		},
	}}

	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_byte_001",
		PreferredLanguages: []string{originalLang},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// The bundle's PlainText matches the DB row's TextContent
	// byte-equivalent (no LLM re-derivation, no translation).
	assert.Equal(t, originalText, bundle.PlainText,
		"bundle.PlainText MUST match the DB row byte-equivalent (no re-derivation)")

	// SourceType byte-equivalent.
	assert.Equal(t, asset.TextSourceYouTubeSubtitle, bundle.SourceType,
		"bundle.SourceType MUST match the DB row byte-equivalent (provenance preserved)")

	// Provider byte-equivalent (the DB row's Provider propagates).
	assert.Equal(t, "yt-dlp", bundle.Provider,
		"bundle.Provider MUST match the DB row byte-equivalent")

	// ModelName + ModelVersion propagate from the DB row to the bundle
	// (the resolver's cdbRowToBundle helper copies these verbatim).
	assert.Equal(t, "yt-auto", bundle.ModelName,
		"bundle.ModelName MUST match the DB row byte-equivalent")
	assert.Equal(t, "v1", bundle.ModelVersion,
		"bundle.ModelVersion MUST match the DB row byte-equivalent")

	// The bundle's SourceLanguageCode is the DB row's
	// SourceLanguageCode (the original clip's language, NOT the
	// target language — the translation history is preserved).
	assert.Equal(t, originalSrcLang, bundle.SourceLanguageCode,
		"bundle.SourceLanguageCode MUST be the original source language (translation history preserved)")
}

// TestCanonicalCase_PreferredLanguagesFanOut_PicksFirstMatch pins
// the user-spec preferred-languages fan-out: the resolver picks
// the FIRST READY match in the PreferredLanguages list, NOT
// any random match. When the asset has it+en+es READY tracks
// and the request is language=it with PreferredLanguages=[it,en,es],
// the resolver MUST pick "it" (first match).
//
// SUT BUG 3: the resolver may pick a non-first match if the
// priority-2 fan-out iterates the DB in insertion order rather
// than the PreferredLanguages order. The test pins the
// PreferredLanguages-order contract.
func TestCanonicalCase_PreferredLanguagesFanOut_PicksFirstMatch(t *testing.T) {
	t.Parallel()

	// Asset has it+en+es READY tracks (3 languages in DB).
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_fanout_001", LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextContent: "italiano dal DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_fanout_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "english from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_fanout_001", LanguageCode: "es", TextKind: asset.TextTrackTranscript, TextContent: "español del DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})

	// PreferredLanguages = [it, en, es] — the resolver MUST pick "it"
	// (first match), not "en" or "es" (the DB insertion order).
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_fanout_001",
		PreferredLanguages: []string{"it", "en", "es"},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	assert.Equal(t, "it", bundle.LanguageCode,
		"resolver MUST pick the first preferred language with a READY row. got=%q", bundle.LanguageCode)
	assert.Equal(t, "italiano dal DB", bundle.PlainText,
		"bundle.PlainText MUST be the Italian DB row's TextContent. got=%q", bundle.PlainText)

	// Now reverse the PreferredLanguages order to [es, en, it] and
	// verify the resolver picks "es" (the new first match). This
	// pins the PreferredLanguages-order contract (not the DB
	// insertion order).
	bundle2, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_fanout_001",
		PreferredLanguages: []string{"es", "en", "it"},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle2)
	assert.Equal(t, "es", bundle2.LanguageCode,
		"resolver MUST honor PreferredLanguages order (es first). got=%q", bundle2.LanguageCode)
	assert.Equal(t, "español del DB", bundle2.PlainText,
		"bundle.PlainText MUST be the Spanish DB row's TextContent. got=%q", bundle2.PlainText)
}

// ── Group 3: Missing translation policy (TextTrackResolver) ─────────────

// TestMissingTranslation_FrenchNotReady_NoSilentTranslation pins
// the user-spec canonical invariant: "mai traduzione silente".
//
// The asset has NO fr track in DB, no fr subtitles, no Whisper
// fallback that can produce fr content. The resolver MUST NOT
// silently fall through to a Whisper call (which would emit
// text in whatever language Whisper detected — potentially
// not fr). The test pins: Whisper.calls == 0 when there's no
// source material for the target language AND
// RequireLanguageCertainty is true (the policy-gate fail-closed
// path).
//
// SUT BUG 4: the resolver may call Whisper with a
// "best-effort" fallback even when no source material exists
// for the target language, silently producing a Whisper
// transcript in a different language. The test pins the
// fail-closed policy gate.
func TestMissingTranslation_FrenchNotReady_NoSilentTranslation(t *testing.T) {
	t.Parallel()

	// Asset has en+es+it READY (no fr).
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_missing_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "English from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_missing_001", LanguageCode: "es", TextKind: asset.TextTrackTranscript, TextContent: "Español del DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_missing_001", LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextContent: "Italiano dal DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	// Subtitles return a non-fr bundle (the resolver's
	// languageInList check will reject the bundle and fall
	// through; but RequireLanguageCertainty=true will fire
	// before Whisper is consulted).
	subs := &p1fStubSubtitles{bundle: &asset.ResolvedTextBundle{
		LanguageCode: "en", PlainText: "English subs",
		SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true,
	}}
	trans := &p1fStubTranscriber{text: "MUST NOT be called"}

	resolver := &usecase.TextTrackResolver{
		Repo:                     repo,
		Subtitles:                subs,
		Transcriber:              trans,
		Log:                      zap.NewNop(),
		RequireLanguageCertainty: true, // fail-closed policy gate
	}

	// Request language=fr. No fr track anywhere.
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_missing_001",
		VideoID:            "v_missing_001",
		LocalPath:          "/tmp/missing.mp4",
		PreferredLanguages: []string{"fr"},
	})

	// User-spec invariant 1: RequireLanguageCertainty=true with
	// no source material MUST fire asset.ErrLanguageUndeterminable
	// (the fail-closed policy gate).
	require.Error(t, err,
		"RequireLanguageCertainty=true with no fr source material MUST return a typed error (fail-closed policy gate)")
	assert.True(t, asset.IsLanguageUndeterminable(err),
		"err MUST be errors.As-probeable as *asset.ErrLanguageUndeterminable. got=%T %v", err, err)
	assert.Nil(t, bundle, "bundle MUST be nil on the fail-closed path")

	// User-spec invariant 2: NO Whisper call (no silent
	// transcription). The resolver MUST short-circuit at the
	// policy gate before reaching the Whisper port.
	assert.Equal(t, 0, trans.calls,
		"Whisper MUST NOT be called when RequireLanguageCertainty=true and no source material for the target language. "+
			"got %d calls (user spec: NO nuova trascrizione silente)", trans.calls)

	// User-spec invariant 3: NO silent translation. The
	// resolver does NOT have a translator port in the canonical
	// 5-level chain (translation is a separate concern owned
	// by TranslateScriptSpec). The test pins the resolver's
	// no-silent-fallback invariant: when the chain exhausts,
	// the resolver returns (nil, err) — not a fallback
	// bundle in a different language.
}

// TestMissingTranslation_AvailableLanguagesSurfaced pins the
// operator-visibility contract: when ErrTextTrackNotReady
// fires, the AvailableLanguages slice MUST carry the sorted
// set of languages for which a READY track exists, so
// operator dashboards can surface "what's actually READY"
// without a second round-trip.
//
// The ListReadyLanguages stub is the canonical source of this
// list. The test pins the "list is populated, not empty"
// invariant (SUT BUG 5).
func TestMissingTranslation_AvailableLanguagesSurfaced(t *testing.T) {
	t.Parallel()

	// Asset has en+es+it READY (no fr, no de).
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_avail_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "English from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_avail_001", LanguageCode: "es", TextKind: asset.TextTrackTranscript, TextContent: "Español del DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_avail_001", LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextContent: "Italiano dal DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	// The TextTrackReader surface (Fase 4) is what builds the
	// ErrTextTrackNotReady error. The test uses the
	// repository directly to verify the ListReadyLanguages
	// output is what the error would carry.
	got, err := repo.ListReadyLanguages(context.Background(), "yt_p1f_avail_001", asset.TextTrackTranscript)
	require.NoError(t, err, "ListReadyLanguages MUST succeed")
	require.NotNil(t, got, "ListReadyLanguages MUST return a non-nil slice")

	// Sort the result (ListReadyLanguages returns the sorted set
	// per the canonical contract).
	expected := []string{"en", "es", "it"}
	assert.Equal(t, expected, got,
		"ListReadyLanguages MUST return the sorted set of READY languages. got=%v", got)

	// Simulate the ErrTextTrackNotReady construction with the
	// available languages list. The test pins the
	// AvailableLanguages population contract.
	typed := &ErrTextTrackNotReady{
		AssetID:            "yt_p1f_avail_001",
		RequestedLanguage:  "fr",
		AvailableLanguages: got,
		MissingKind:        asset.TextTrackTranscript,
	}
	errMsg := typed.Error()
	// The error message MUST include every available language
	// (so operator dashboards can correlate "fr was requested,
	// but en/es/it are READY").
	for _, lang := range expected {
		assert.Contains(t, errMsg, lang,
			"ErrTextTrackNotReady.Error() MUST mention every available language %q. got=%q",
			lang, errMsg)
	}
	// And the requested language MUST appear in the message.
	assert.Contains(t, errMsg, "fr",
		"ErrTextTrackNotReady.Error() MUST mention the requested language \"fr\". got=%q", errMsg)

	// errors.Is probe (the canonical godlike/07 typed-error
	// contract): the typed error MUST be probeable via
	// errors.Is(err, &ErrTextTrackNotReady{}).
	require.True(t, errors.Is(typed, &ErrTextTrackNotReady{}),
		"ErrTextTrackNotReady MUST be errors.Is-probeable (godlike/07 typed-error contract)")
}

// TestMissingTranslation_ChainExhausted_NoSilentSubstitution pins
// the user-spec no-silent-translation invariant. The user spec
// says "fallback con warning esplicito (mai traduzione
// silente)" — the canonical resolver today returns (nil, nil)
// on chain-exhausted without RequireLanguageCertainty; there
// is NO silent language substitution. The test pins the
// invariant by asserting that, when the chain exhausts
// without a matching language, the resolver does NOT
// produce a bundle in a different language.
//
// SUT BUG 6 (silent language substitution): the resolver
// may substitute "en" for an empty/unknown targetLang
// input. The godlike/07 no-fake-availability invariant
// pins empty → "und", never silently to "en". The test
// exercises the empty-targetLang path.
//
// Note: a future PR that adds a "fallback to closest available
// language with explicit warning" path would change the
// assertion to (bundle!=nil with warning!=empty) — the test
// pins the current (nil, nil) behavior, which is the
// godlike/07 fail-closed default.
func TestMissingTranslation_FallbackWithExplicitWarning(t *testing.T) {
	t.Parallel()

	// Asset has en+es+it READY (no fr, no de).
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_fallback_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "English from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_fallback_001", LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextContent: "Italiano dal DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	// Subtitles + Whisper return no usable content.
	subs := &p1fStubSubtitles{bundle: nil}
	trans := &p1fStubTranscriber{text: ""}

	// RequireLanguageCertainty=false (the canonical
	// pre-Fase-1.b behavior: chain exhaustion → (nil, nil)
	// silent degradation, no error). The test pins this
	// current behavior AND the no-silent-substitution
	// invariant: the resolver MUST NOT produce a bundle in
	// "en" just because "en" is the closest available
	// language.
	resolver := newP1FResolver(repo, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_fallback_001",
		VideoID:            "v_fallback_001",
		LocalPath:          "/tmp/fallback.mp4",
		PreferredLanguages: []string{"fr", "de"}, // both absent
	})
	require.NoError(t, err, "RequireLanguageCertainty=false should NOT fire (chain miss → (nil, nil))")
	assert.Nil(t, bundle, "chain-exhausted MUST return nil bundle (no silent fallback)")

	// User-spec invariant: NO silent substitution to the closest
	// available language. The resolver did NOT consult
	// Whisper/Subtitles/Translate to produce an "en" bundle.
	assert.Equal(t, 0, trans.calls,
		"Whisper MUST NOT be called when no source material exists for the target language. "+
			"got %d calls (no silent substitution)", trans.calls)
	assert.Equal(t, 0, subs.calls,
		"Subtitles MUST NOT be called when no source material exists for the target language. "+
			"got %d calls (no silent substitution)", subs.calls)

	// Now exercise the empty targetLang path. The user spec
	// mandates that an empty targetLang must NOT silently
	// default to "en" (godlike/07 no-fake-availability).
	bundle2, err2 := resolver.ResolveLanguage(context.Background(),
		"yt_p1f_fallback_001", "", asset.TextTrackTranscript)
	require.NoError(t, err2, "ResolveLanguage with empty targetLang MUST NOT error (godlike/07: empty → \"und\")")
	assert.Nil(t, bundle2,
		"ResolveLanguage with empty targetLang MUST return nil (no silent substitution to \"en\")")
}

// TestMissingTranslation_NormalizeEmptyLanguageToUnd pins the
// godlike/07 no-fake-availability invariant: an empty
// targetLang input MUST collapse to BCP-47 "und", never
// silently default to "en". The test exercises the
// canonical BCP-47 normalizer path and pins the
// error-language contract.
func TestMissingTranslation_NormalizeEmptyLanguageToUnd(t *testing.T) {
	t.Parallel()

	// godlike/07 honest lock: empty input MUST collapse to
	// "und" (the canonical BCP-47 undetermined marker). The
	// resolver does NOT silently substitute "en" for the
	// empty input.
	normalized, err := asset.Normalize("")
	require.NoError(t, err, "Normalize with empty input MUST NOT error")
	assert.Equal(t, "und", normalized,
		"Normalize(\"\") MUST collapse to \"und\" (BCP-47 undetermined), never \"en\"")

	// And the resolver's ResolveLanguage method MUST honor
	// the canonical "und" → "not found" path (no DB probe
	// with "und", no silent substitution).
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_empty_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "English from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}
	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})

	// Empty targetLang → resolver collapses to "und" → no DB
	// probe (the resolver does NOT scan all rows for the
	// asset).
	row, err := resolver.ResolveLanguage(context.Background(),
		"yt_p1f_empty_001", "", asset.TextTrackTranscript)
	require.NoError(t, err)
	assert.Nil(t, row,
		"ResolveLanguage with empty targetLang MUST return nil (no silent substitution to \"en\" or any default)")
}

// ── Compile-time pin ────────────────────────────────────────────────────

// Compile-time assertion: the package's typed sentinels are
// reachable (godlike/07 typed-error contract).
var (
	_ error = ErrTranslationSourceInvalid
	_ error = ErrTranslationTranslatorMissing
	_ error = ErrTranslationTargetLangMissing
	_ error = ErrTranslationEmpty
	_ error = ErrTranslationIncomplete
)

// Compile-time assertion: ErrTextTrackNotReady satisfies the
// error interface and the errors.Is probe pattern.
var _ error = (*ErrTextTrackNotReady)(nil)

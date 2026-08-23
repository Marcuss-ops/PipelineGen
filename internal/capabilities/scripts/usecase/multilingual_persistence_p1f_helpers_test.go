// multilingual_persistence_p1f_helpers_test.go
//
// Shared helpers for Group 1 cross-language consistency tests.
// Extracted atomically from multilingual_persistence_p1f_test.go (P1F, 2026-07-04).
// Sourced by multilingual_persistence_p1f_multilingual_8scenes_test.go via same usecase package.
//
// godlike/06 SSOT: helper funcs are the canonical SOLE owner of fixtures
// for Group 1 (Pacquiao-Broner 8-scene fixture build, evidence helper,
// per-language suffix translator hook, semantic-marker map). No
// duplicate definitions in the original file post-extract.

package usecase

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
			"Final round, Pacquiao and Broner give everything, the bell rings",
			"Round 12 — The end of the match; Pacquiao and Broner empty the tank together, the final bell rings.",
			"Round 12: Pacquiao vs Broner - il finale del match"),
		makeScene("scene-8", "clip-post", "Post-match — Verdict announcement",
			"https://drive.google.com/file/d/pacquiao-broner-post/view",
			"img-post",
			"Ring announcer with the scorecards; Pacquiao and Broner await the verdict",
			"Post-match — The official verdict is announced; Pacquiao wins by unanimous decision over Broner.",
			"Post-match: Pacquiao batte Broner - annuncio del verdetto"),
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

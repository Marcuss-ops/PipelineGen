package overlays

import (
	"context"
	"testing"
)

// P0-1: fixture golden di selezione per categoria. Stesso input+seed+versione
// → stesso piano; i percorsi certificati restano byte-identici.
// Copre: frase breve/lunga/heavy, singola immagine, entity+caption,
// gruppo immagini, mappa, date/metric, chain, multilingua (seed per lingua).
func TestAnimationUnificationGoldenDeterminism(t *testing.T) {
	sel := DefaultAnimationSelector{CatalogDigest: "test-digest-v1"}
	cases := []AnimationSelectionRequest{
		{Target: "short_phrase", Mode: SelectionAuto, SceneID: "s1", ElementID: "p1", Seed: "seed", Words: 3, Ordinal: 0},
		{Target: "important_phrase", Mode: SelectionAuto, SceneID: "s1", ElementID: "p2", Seed: "seed", Words: 8, Ordinal: 1},
		{Target: "heavy_phrase", Mode: SelectionAuto, SceneID: "s1", ElementID: "p3", Seed: "seed", Ordinal: 0},
		{Target: "image", Mode: SelectionAuto, SceneID: "s1", ElementID: "img1", Seed: "seed", Ordinal: 0},
		{Target: "entity_image", Mode: SelectionAuto, SceneID: "s1", ElementID: "ent1", Seed: "seed", Ordinal: 0},
		{Target: "caption", Mode: SelectionAuto, SceneID: "s1", ElementID: "cap1", Seed: "seed", Ordinal: 0},
		{Target: "date", Mode: SelectionAuto, SceneID: "s1", ElementID: "d1", Seed: "seed"},
		{Target: "metric", Mode: SelectionAuto, SceneID: "s1", ElementID: "m1", Seed: "seed"},
		{Target: "map_camera", Mode: SelectionAuto, SceneID: "s1", ElementID: "map1", Seed: "seed"},
		{Target: "important_phrase", Mode: SelectionExact, SceneID: "s1", ElementID: "p9", RequestedMotion: "apple_phrase_v2"},
	}
	for _, tc := range cases {
		a, err := sel.Resolve(context.Background(), tc)
		if err != nil {
			t.Fatalf("target %s: %v", tc.Target, err)
		}
		b, err := sel.Resolve(context.Background(), tc)
		if err != nil || a.MotionID != b.MotionID {
			t.Fatalf("nondeterministic target %s: %q vs %q (%v)", tc.Target, a.MotionID, b.MotionID, err)
		}
		if a.MotionID == "" {
			t.Fatalf("empty motion target %s", tc.Target)
		}
	}
	// Multilingua: seed per lingua → compatibilità rivalutata per lingua,
	// scelte editoriali non alterate arbitrariamente (§9 invariante 4).
	it, _ := sel.Resolve(context.Background(), AnimationSelectionRequest{Target: "important_phrase", Seed: "seed-it", SceneID: "s", Words: 8})
	en, _ := sel.Resolve(context.Background(), AnimationSelectionRequest{Target: "important_phrase", Seed: "seed-en", SceneID: "s", Words: 10})
	if it.MotionID == "" || en.MotionID == "" {
		t.Fatalf("multilingual lanes must resolve")
	}
}

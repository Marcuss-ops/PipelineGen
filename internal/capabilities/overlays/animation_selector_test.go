package overlays

import (
	"context"
	"strings"
	"testing"
)

func TestAnimationSelectorExactNeverSubstituted(t *testing.T) {
	sel := DefaultAnimationSelector{CatalogDigest: "test-digest"}
	got, err := sel.Resolve(context.Background(), AnimationSelectionRequest{
		Target: "important_phrase", Mode: SelectionExact,
		SceneID: "scene_004", ElementID: "phrase_1",
		RequestedMotion: "apple_phrase_v2",
	})
	if err != nil {
		t.Fatalf("exact resolve: %v", err)
	}
	if got.MotionID != "apple_phrase_v2" || got.Mode != SelectionExact {
		t.Fatalf("exact must pass through: %+v", got)
	}
	if _, err := sel.Resolve(context.Background(), AnimationSelectionRequest{
		Target: "important_phrase", Mode: SelectionExact, SceneID: "s",
	}); err == nil {
		t.Fatalf("exact without motion must fail closed")
	}
}

func TestAnimationSelectorAutoDeterministic(t *testing.T) {
	sel := DefaultAnimationSelector{}
	a, err := sel.Resolve(context.Background(), AnimationSelectionRequest{
		Target: "important_phrase", Mode: SelectionAuto,
		SceneID: "scene_005", Seed: "seed-1", Words: 8, Ordinal: 0,
	})
	if err != nil || a.MotionID == "" {
		t.Fatalf("auto phrase: %+v %v", a, err)
	}
	b, _ := sel.Resolve(context.Background(), AnimationSelectionRequest{
		Target: "important_phrase", Mode: SelectionAuto,
		SceneID: "scene_005", Seed: "seed-1", Words: 8, Ordinal: 0,
	})
	if a.MotionID != b.MotionID {
		t.Fatalf("determinism: %q vs %q", a.MotionID, b.MotionID)
	}
	cap, err := sel.Resolve(context.Background(), AnimationSelectionRequest{
		Target: "caption", Mode: SelectionAuto, Seed: "seed-1", SceneID: "s", Ordinal: 2,
	})
	if err != nil || cap.MotionID == "" {
		t.Fatalf("auto caption: %+v %v", cap, err)
	}
}

func TestResolveAnimationSelectionsChainTransparent(t *testing.T) {
	// Container con MotionID deve fallire: le motion vivono nei segmenti.
	bad := []OverlayItem{{ID: "c1", SceneID: "s", Kind: "phrase_chain", MotionID: "x"}}
	if err := ResolveAnimationSelections(bad); err == nil {
		t.Fatalf("chain container with MotionID must fail")
	}
	good := []OverlayItem{{ID: "c1", SceneID: "s", Kind: "phrase_chain"}}
	if err := ResolveAnimationSelections(good); err != nil {
		t.Fatalf("empty chain container ok: %v", err)
	}
	// Composite parent senza MotionID ma child senza MotionID deve fallire.
	badParent := []OverlayItem{{
		ID: "e1", SceneID: "s", Kind: string(KindEntityImage),
		ImageLayers: []OverlayImageLayer{{ID: "l1"}},
	}}
	if err := ResolveAnimationSelections(badParent); err == nil {
		t.Fatalf("child without motion must fail")
	}
	if !strings.Contains(string(KindPhraseChain), "chain") {
		t.Fatalf("sanity")
	}
}

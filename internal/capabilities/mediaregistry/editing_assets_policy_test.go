package mediaregistry

import (
	"errors"
	"fmt"
	"testing"
)

func TestDefaultEditingAssetsPolicyIsValid(t *testing.T) {
	policy := DefaultEditingAssetsPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := len(policy.Backgrounds.Pool); got != 11 {
		t.Errorf("background pool = %d, want 11", got)
	}
	if got := len(policy.BGM.Pool); got != 6 {
		t.Errorf("bgm pool = %d, want 6", got)
	}
	if got := len(policy.SFX.TransitionPool); got != 9 {
		t.Errorf("transition pool = %d, want 9 (whop1..6 + bound whoosh1..3)", got)
	}
	if policy.BGM.GainDB != -28 || policy.BGM.DuckGainDB != -32 || policy.SFX.GainDB != -12 {
		t.Errorf("unexpected audio defaults: %+v", policy.BGM)
	}
	if !policy.BGM.Loop || !policy.BGM.DuckUnderVoiceover {
		t.Errorf("BGM defaults must loop and duck under voiceover: %+v", policy.BGM)
	}
}

func TestEditingAssetsPolicySelectionIsDeterministicAndInPool(t *testing.T) {
	policy := DefaultEditingAssetsPolicy()
	bgPool := poolSet(policy.Backgrounds.Pool)
	bgmPool := poolSet(policy.BGM.Pool)
	sfxPool := poolSet(policy.SFX.TransitionPool)

	for i := 0; i < 16; i++ {
		seed := fmt.Sprintf("matt-damon-scene-%d", i)
		bg, err := policy.SelectBackground(seed)
		if err != nil {
			t.Fatalf("SelectBackground(%q): %v", seed, err)
		}
		if !bgPool[bg.ID] {
			t.Fatalf("background %q is outside the pool", bg.ID)
		}
		again, err := policy.SelectBackground(seed)
		if err != nil || again != bg {
			t.Fatalf("background selection is not deterministic for %q: %v vs %v", seed, again, bg)
		}
		bgm, err := policy.SelectBGM(seed)
		if err != nil {
			t.Fatalf("SelectBGM(%q): %v", seed, err)
		}
		if !bgmPool[bgm.Alias] {
			t.Fatalf("bgm %q is outside the pool", bgm.Alias)
		}
		if bgm.Family != "music" {
			t.Fatalf("selected BGM %q family = %q", bgm.Alias, bgm.Family)
		}
		sfx, err := policy.SelectTransitionSFX(seed)
		if err != nil {
			t.Fatalf("SelectTransitionSFX(%q): %v", seed, err)
		}
		if !sfxPool[sfx.Alias] {
			t.Fatalf("sfx %q is outside the pool", sfx.Alias)
		}
		if sfx.Family != "transition" {
			t.Fatalf("selected SFX %q family = %q", sfx.Alias, sfx.Family)
		}
	}
}

func TestEditingAssetsPolicyFailsClosed(t *testing.T) {
	empty := DefaultEditingAssetsPolicy()
	empty.BGM.Pool = nil
	if _, err := empty.SelectBGM("seed"); !errors.Is(err, ErrEmptyEditingPool) {
		t.Fatalf("empty pool error = %v, want ErrEmptyEditingPool", err)
	}

	foreign := DefaultEditingAssetsPolicy()
	foreign.Backgrounds.Pool = []string{"drive-background-99"}
	if _, err := foreign.SelectBackground("seed"); !errors.Is(err, ErrUnknownEditingAsset) {
		t.Fatalf("unknown asset error = %v, want ErrUnknownEditingAsset", err)
	}

	wrongKind := DefaultEditingAssetsPolicy()
	wrongKind.SFX.TransitionPool = []string{"bgm1"}
	if err := wrongKind.Validate(); !errors.Is(err, ErrEditingAssetKindMismatch) {
		t.Fatalf("wrong-kind error = %v, want ErrEditingAssetKindMismatch", err)
	}

	positiveGain := DefaultEditingAssetsPolicy()
	positiveGain.SFX.GainDB = 3
	if err := positiveGain.Validate(); err == nil {
		t.Fatal("positive gain must be rejected")
	}
}

func TestParseEditingAssetsPolicyRejectsUnknownFields(t *testing.T) {
	if _, err := ParseEditingAssetsPolicy([]byte("editing_assets:\n  bogus: 1\n")); err == nil {
		t.Fatal("an unknown policy field must fail closed")
	}
}

func poolSet(pool []string) map[string]bool {
	out := make(map[string]bool, len(pool))
	for _, id := range pool {
		out[id] = true
	}
	return out
}

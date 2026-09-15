package script

import (
	"encoding/json"
	"strings"
	"testing"
)

// canonicalAudioAliasesForTest is the vocabulary the canonical editorial catalog
// (internal/capabilities/mediaregistry/editorial_catalog.go) binds. It is
// duplicated here on purpose: the drift gate on the catalog side compares that
// package against this file, and this test fails if the kernel declaration
// drifts in the other direction. Both sides failing loudly is the point — the
// declaration is the contract, the catalog is the binding.
var canonicalAudioAliasesForTest = []string{
	"bgm1", "bgm2", "bgm3", "bgm4", "bgm5", "bgm6",
	"whop1", "whop2", "whop3", "whop4", "whop5", "whop6",
	// The bound members of the whoosh family: bound so random_whoosh resolves.
	"whoosh1", "whoosh2", "whoosh3",
}

func TestBuiltInAudioCanonicalAliasesMatchTheEditorialCatalog(t *testing.T) {
	got := BuiltInAudioCanonicalAliases()
	if len(got) != len(canonicalAudioAliasesForTest) {
		t.Fatalf("canonical built-in audio aliases = %d (%v), want %d (%v)",
			len(got), got, len(canonicalAudioAliasesForTest), canonicalAudioAliasesForTest)
	}
	want := make(map[string]struct{}, len(canonicalAudioAliasesForTest))
	for _, alias := range canonicalAudioAliasesForTest {
		want[alias] = struct{}{}
	}
	for _, alias := range got {
		if _, ok := want[alias]; !ok {
			t.Errorf("canonical alias %q is not bound by the editorial catalog", alias)
		}
	}
	for alias := range want {
		kind, ok := BuiltInAudioAliasKind(alias)
		if !ok {
			t.Errorf("editorial alias %q is missing from the built-in vocabulary", alias)
		}
		if alias[:3] == "bgm" && kind != BuiltInAudioBackgroundMusic {
			t.Errorf("alias %q classified %q, want %q", alias, kind, BuiltInAudioBackgroundMusic)
		}
		if alias[:4] == "whop" && kind != BuiltInAudioSoundEffect {
			t.Errorf("alias %q classified %q, want %q", alias, kind, BuiltInAudioSoundEffect)
		}
		if strings.HasPrefix(alias, "whoosh") && kind != BuiltInAudioSoundEffect {
			t.Errorf("whoosh alias %q classified %q, want %q", alias, kind, BuiltInAudioSoundEffect)
		}
	}
}

func TestBuiltInAudioAliasKindNormalizesAndRejectsUnknown(t *testing.T) {
	cases := []struct {
		id   string
		kind BuiltInAudioKind
		ok   bool
	}{
		{"bgm3", BuiltInAudioBackgroundMusic, true},
		{"  BGM3  ", BuiltInAudioBackgroundMusic, true},
		{"whop1", BuiltInAudioSoundEffect, true},
		{"WHOP1", BuiltInAudioSoundEffect, true},
		{"whoosh4", BuiltInAudioSoundEffect, true},
		{"random_whoosh", BuiltInAudioSoundEffect, true},
		{"", "", false},
		{"classic1", "", false},
		// The retired prefix rule accepted the whole `whoosh*`/`whop*` space,
		// so a caller asset id like this inherited the built-in attenuation.
		// The vocabulary is closed now: only declared aliases are built-in.
		{"whoosh_custom_hit", "", false},
		{"whop99", "", false},
		{"bgm7", "", false},
	}
	for _, tc := range cases {
		kind, ok := BuiltInAudioAliasKind(tc.id)
		if ok != tc.ok || kind != tc.kind {
			t.Errorf("BuiltInAudioAliasKind(%q) = (%q, %v), want (%q, %v)", tc.id, kind, ok, tc.kind, tc.ok)
		}
	}
}

func TestBuiltInAudioVocabularyHasNoCanonicalCompatOverlap(t *testing.T) {
	for alias := range builtInCompatAliases {
		if _, ok := builtInCanonicalAliases[alias]; ok {
			t.Errorf("alias %q is declared both canonical and compat", alias)
		}
	}
	if _, ok := builtInCanonicalAliases[RandomWhooshDirective]; ok {
		t.Errorf("%q is a resolver directive and must not be a canonical alias", RandomWhooshDirective)
	}
}

func TestBuiltInWhooshAliasesAreDeclaredBuiltIn(t *testing.T) {
	aliases := BuiltInWhooshAliases()
	if len(aliases) == 0 {
		t.Fatal("whoosh family is empty")
	}
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		if _, dup := seen[alias]; dup {
			t.Errorf("duplicate whoosh alias %q", alias)
		}
		seen[alias] = struct{}{}
		if kind, ok := BuiltInAudioAliasKind(alias); !ok || kind != BuiltInAudioSoundEffect {
			t.Errorf("whoosh family member %q is not a declared built-in SFX (kind=%q ok=%v)", alias, kind, ok)
		}
	}
	// The resolver indexes the family, so index 0 must stay whoosh1.
	if aliases[0] != "whoosh1" {
		t.Errorf("whoosh family must start at whoosh1, got %q", aliases[0])
	}
}

// TestWireDecodingAppliesBuiltInDefaultsOnlyForDeclaredAliases locks the
// behaviour the retired prefix predicates used to decide: a built-in BGM loops
// and sits at -30 dB unless the caller says otherwise, a built-in one-shot is
// attenuated unless the caller says otherwise, and a non-built-in id keeps the
// neutral 0 dB because its gain is the caller's business.
func TestWireDecodingAppliesBuiltInDefaultsOnlyForDeclaredAliases(t *testing.T) {
	t.Run("bgm default", func(t *testing.T) {
		var intent BackgroundMusicIntent
		if err := json.Unmarshal([]byte(`{"asset_id":"bgm3"}`), &intent); err != nil {
			t.Fatal(err)
		}
		if !intent.Loop || intent.GainDB != -30 {
			t.Fatalf("built-in BGM defaults = loop:%v gain:%v, want loop:true gain:-30", intent.Loop, intent.GainDB)
		}
	})

	t.Run("bgm explicit values win", func(t *testing.T) {
		var intent BackgroundMusicIntent
		if err := json.Unmarshal([]byte(`{"asset_id":"bgm3","loop":false,"gain_db":0}`), &intent); err != nil {
			t.Fatal(err)
		}
		if intent.Loop || intent.GainDB != 0 {
			t.Fatalf("explicit BGM values = loop:%v gain:%v, want loop:false gain:0", intent.Loop, intent.GainDB)
		}
	})

	for _, id := range []string{"whop1", "whoosh4", "random_whoosh"} {
		t.Run("sfx default "+id, func(t *testing.T) {
			var intent SoundEffectIntent
			if err := json.Unmarshal([]byte(`{"asset_id":"`+id+`","at_ms":1000}`), &intent); err != nil {
				t.Fatal(err)
			}
			if intent.GainDB != -30 {
				t.Fatalf("%s built-in SFX default gain = %v, want -30", id, intent.GainDB)
			}
		})
	}

	t.Run("caller asset keeps neutral gain", func(t *testing.T) {
		var intent SoundEffectIntent
		if err := json.Unmarshal([]byte(`{"asset_id":"whoosh_custom_hit","at_ms":1000}`), &intent); err != nil {
			t.Fatal(err)
		}
		if intent.GainDB != 0 {
			t.Fatalf("caller asset gain = %v, want 0 (the built-in attenuation must not leak onto unknown ids)", intent.GainDB)
		}
	})

	t.Run("non built-in bgm keeps neutral gain", func(t *testing.T) {
		var intent BackgroundMusicIntent
		if err := json.Unmarshal([]byte(`{"asset_id":"bgm_documentary_01"}`), &intent); err != nil {
			t.Fatal(err)
		}
		if intent.Loop || intent.GainDB != 0 {
			t.Fatalf("caller BGM defaults = loop:%v gain:%v, want loop:false gain:0", intent.Loop, intent.GainDB)
		}
	})
}

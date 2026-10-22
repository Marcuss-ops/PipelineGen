package linguistics

import (
	"path/filepath"
	"runtime"
	"testing"
)

// testRegistry loads the repository lexicon the same way the composition root
// does; the tests below assert the migrated named sets, not a fixture copy.
func testRegistry(t *testing.T) *LexiconRegistry {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../config/lexicons"))
	registry, err := NewLexiconRegistry(root)
	if err != nil {
		t.Fatalf("NewLexiconRegistry(%q): %v", root, err)
	}
	return registry
}

func TestWordSetsLoadFromRepositoryData(t *testing.T) {
	registry := testRegistry(t)

	checks := []struct {
		lang  string
		name  string
		words []string
	}{
		{"en", "subordinate_markers", []string{"after", "with", "upon"}},
		{"en", "pronouns", []string{"he", "their", "us"}},
		{"it", "pronouns", []string{"lui", "sua", "loro"}},
		{"it", "articles", []string{"il", "gli", "le"}},
		{"it", "possessives", []string{"suo", "sue"}},
		{"it", "pronoun_clitics", []string{"gli", "ne", "lo"}},
		{"it", "pro_drop_aux", []string{"ha", "è", "furono"}},
		{"en", "contrast_openers", []string{"unlike", "instead"}},
		{"it", "contrast_openers", []string{"invece", "anziché"}},
		{"fallback", "numeric_overlay_words", []string{"zero", "mille", "três"}},
		{"fallback", "numeric_overlay_context", []string{"the", "gennaio", "kg"}},
	}
	for _, check := range checks {
		set := registry.WordSet(check.lang, check.name)
		if len(set) == 0 {
			t.Fatalf("WordSet(%q, %q) is empty", check.lang, check.name)
		}
		for _, word := range check.words {
			if _, ok := set[word]; !ok {
				t.Errorf("WordSet(%q, %q) is missing %q", check.lang, check.name, word)
			}
		}
	}
}

func TestPhraseSetsAreTokenisedAtLoad(t *testing.T) {
	registry := testRegistry(t)

	phraseSet := registry.PhraseSet("en", "negative_phrases")
	if len(phraseSet) == 0 {
		t.Fatal("PhraseSet(en, negative_phrases) is empty")
	}
	want := map[string]bool{"instead of": false, "as opposed to": false}
	for _, phrase := range phraseSet {
		joined := ""
		for i, token := range phrase {
			if i > 0 {
				joined += " "
			}
			joined += token
		}
		if _, ok := want[joined]; ok {
			want[joined] = true
		}
	}
	for phrase, found := range want {
		if !found {
			t.Errorf("PhraseSet(en, negative_phrases) is missing %q", phrase)
		}
	}

	italian := registry.PhraseSet("it", "negative_phrases")
	if len(italian) == 0 {
		t.Fatal("PhraseSet(it, negative_phrases) is empty")
	}
}

func TestUnknownWordSetAndLanguageAreNil(t *testing.T) {
	registry := testRegistry(t)
	if set := registry.WordSet("en", "does_not_exist"); set != nil {
		t.Errorf("WordSet for an unknown set = %v, want nil", set)
	}
	if set := registry.WordSet("xx", "pronouns"); set != nil {
		t.Errorf("WordSet for an unknown language = %v, want nil", set)
	}
	if phrases := registry.PhraseSet("en", "does_not_exist"); phrases != nil {
		t.Errorf("PhraseSet for an unknown set = %v, want nil", phrases)
	}
}

// TestResolvedProfileDeepCopiesNamedSets guards the clone contract: a caller
// that mutates a resolved profile must not change the registry.
func TestResolvedProfileDeepCopiesNamedSets(t *testing.T) {
	registry := testRegistry(t)
	profile, err := registry.ResolveRequired("en")
	if err != nil {
		t.Fatalf("ResolveRequired(en): %v", err)
	}
	profile.WordSets["pronouns"]["injected"] = struct{}{}
	profile.PhraseSets["negative_phrases"][0][0] = "injected"

	if _, ok := registry.WordSet("en", "pronouns")["injected"]; ok {
		t.Error("mutating a resolved profile changed the registry word set")
	}
	if got := registry.PhraseSet("en", "negative_phrases")[0][0]; got == "injected" {
		t.Error("mutating a resolved profile changed the registry phrase set")
	}
}

package linguistics

import "testing"

// testRegistry loads the Rust-generated snapshot the same way the composition
// root does; the tests below assert the migrated named sets, not a fixture copy.
func testRegistry(t *testing.T) *Profiles {
	t.Helper()
	registry, err := NewProfiles()
	if err != nil {
		t.Fatalf("NewProfiles: %v", err)
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

func TestUnknownWordSetAndLanguageAreNil(t *testing.T) {
	registry := testRegistry(t)
	if set := registry.WordSet("en", "does_not_exist"); set != nil {
		t.Errorf("WordSet for an unknown set = %v, want nil", set)
	}
	if set := registry.WordSet("xx", "pronouns"); set != nil {
		t.Errorf("WordSet for an unknown language = %v, want nil", set)
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

	if _, ok := registry.WordSet("en", "pronouns")["injected"]; ok {
		t.Error("mutating a resolved profile changed the registry word set")
	}
}

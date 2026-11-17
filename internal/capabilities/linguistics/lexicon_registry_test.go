package linguistics

import "testing"

func TestNewProfilesLoadsTheGeneratedRustSnapshot(t *testing.T) {
	profiles, err := NewProfiles()
	if err != nil {
		t.Fatalf("NewProfiles: %v", err)
	}
	if profiles.ProfileCount() < 10 {
		t.Fatalf("ProfileCount = %d, want the generated language set", profiles.ProfileCount())
	}
	for _, language := range []string{"de", "en", "es", "fr", "it", "pl", "pt", "ru", "tr"} {
		if !profiles.HasProfile(language) {
			t.Errorf("missing generated profile %q", language)
		}
	}
	if profiles.Source() == "" {
		t.Error("Source is empty; the snapshot must name its Rust source")
	}
	if profiles.Version() == "" {
		t.Error("Version is empty")
	}
}

func TestSharedLanguagesShipTheGeneratedSets(t *testing.T) {
	profiles := MustNewProfiles()
	for _, language := range []string{"de", "en", "es", "fr", "it", "pt"} {
		if !profiles.HasProfile(language) {
			t.Errorf("missing shared language %q", language)
			continue
		}
		if len(profiles.StopWords(language)) == 0 {
			t.Errorf("%s stop words are empty", language)
		}
		if len(profiles.FunctionWords(language)) == 0 {
			t.Errorf("%s function words are empty", language)
		}
	}
}

func TestProfilesResolveNormalizesLanguageTags(t *testing.T) {
	profiles := MustNewProfiles()
	for _, tag := range []string{"en", "en-US", "EN", " en-GB "} {
		profile, err := profiles.ResolveRequired(tag)
		if err != nil {
			t.Errorf("ResolveRequired(%q): %v", tag, err)
			continue
		}
		if len(profile.StopWords) == 0 {
			t.Errorf("ResolveRequired(%q) returned no stop words", tag)
		}
	}
	if _, err := profiles.ResolveRequired("zz"); err == nil {
		t.Fatal("expected an unknown language to fail closed")
	}
}

func TestProfilesReturnDefensiveCopies(t *testing.T) {
	profiles := MustNewProfiles()
	first := profiles.StopWords("en")
	first["mutated"] = struct{}{}
	if _, ok := profiles.StopWords("en")["mutated"]; ok {
		t.Fatal("mutating a resolved stop-word set changed the snapshot")
	}
}

func TestPhraseStopWordsUnionsProfileAndFallsBack(t *testing.T) {
	profiles := MustNewProfiles()
	en := profiles.PhraseStopWords("en")
	for _, word := range []string{"the", "and", "of"} {
		if _, ok := en[word]; !ok {
			t.Errorf("en phrase stop words must contain %q", word)
		}
	}

	uncovered := profiles.PhraseStopWords("xx-YY")
	for _, word := range []string{"by", "della", "los"} {
		if _, ok := uncovered[word]; !ok {
			t.Errorf("an unenumerated language must use the cross-linguistic fallback profile; missing %q", word)
		}
	}
}

func TestProfilesExposeNamedSetsAndMorphology(t *testing.T) {
	profiles := MustNewProfiles()
	for _, check := range []struct {
		language string
		set      string
	}{
		{"en", "pronouns"},
		{"it", "articles"},
		{"it", "pronoun_clitics"},
		{"fallback", "numeric_overlay_words"},
	} {
		if set := profiles.WordSet(check.language, check.set); len(set) == 0 {
			t.Errorf("WordSet(%q, %q) is empty", check.language, check.set)
		}
	}
	if len(profiles.VerbSuffixes("it")) == 0 {
		t.Error("it verb suffixes are empty")
	}
	if len(profiles.NegativeParticles("en")) == 0 {
		t.Error("en negative particles are empty")
	}
	if len(profiles.VisualVerbs("en")) == 0 {
		t.Error("en visual verbs are empty")
	}
	if profiles.PhrasePolicy("it").MaxWords != 6 {
		t.Errorf("it phrase policy max_words = %d, want 6", profiles.PhrasePolicy("it").MaxWords)
	}
}

func TestProfilesVersionIsStable(t *testing.T) {
	first := MustNewProfiles()
	second := MustNewProfiles()
	if first.Version() != second.Version() {
		t.Fatalf("version is not deterministic: %q != %q", first.Version(), second.Version())
	}
}

func TestSetDefaultProfilesRejectsNil(t *testing.T) {
	if err := SetDefaultProfiles(nil); err == nil {
		t.Fatal("expected nil default profiles to return an error")
	}
}

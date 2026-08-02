package linguistics

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLexiconFile(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	root := filepath.Dir(dir)
	if filepath.Base(dir) != "fallback" {
		fallbackDir := filepath.Join(root, "fallback")
		if err := os.MkdirAll(fallbackDir, 0755); err != nil {
			t.Fatal(err)
		}
		fallbackPath := filepath.Join(fallbackDir, "stopwords.txt")
		if _, err := os.Stat(fallbackPath); os.IsNotExist(err) {
			if err := os.WriteFile(fallbackPath, []byte("the\nand\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		f.WriteString(line + "\n")
	}
}

func TestLexiconRegistry_LoadsStopwords(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "stopwords.txt",
		"the", "and", "for", "a", "an",
	)
	writeLexiconFile(t, enDir, "function_words.txt",
		"the", "a", "an", "of",
	)
	writeLexiconFile(t, enDir, "verb_morphology.txt",
		"ing", "ed",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	enProfile := r.Resolve("en")
	if enProfile == nil {
		t.Fatal("expected non-nil profile for en")
	}
	if _, ok := enProfile.StopWords["the"]; !ok {
		t.Error("expected 'the' in en stopwords")
	}
	if _, ok := enProfile.StopWords["nonexistent"]; ok {
		t.Error("did not expect 'nonexistent' in en stopwords")
	}
	if _, ok := enProfile.FunctionWords["of"]; !ok {
		t.Error("expected 'of' in en function_words")
	}
	if len(enProfile.VerbSuffixes) != 2 || enProfile.VerbSuffixes[0] != "ing" {
		t.Errorf("verb suffixes = %v, want [ing ed]", enProfile.VerbSuffixes)
	}
}

func TestLexiconRegistry_ReturnsDefensiveCopies(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "stopwords.txt", "the")

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	first := r.StopWords("en")
	first["mutated"] = struct{}{}

	second := r.StopWords("en")
	if _, ok := second["mutated"]; ok {
		t.Fatal("expected StopWords to return a defensive copy")
	}
}

func TestLexiconRegistry_Fallback(t *testing.T) {
	dir := t.TempDir()
	itDir := filepath.Join(dir, "it")
	writeLexiconFile(t, itDir, "stopwords.txt", "il", "la", "lo")

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	// Italian should resolve to the it profile.
	itProfile := r.Resolve("it")
	if _, ok := itProfile.StopWords["il"]; !ok {
		t.Error("expected 'il' in it stopwords")
	}

	if _, err := r.ResolveRequired("fr"); err == nil {
		t.Fatal("expected missing language to return an error")
	}
}

func TestLexiconRegistry_LanguageTagNormalization(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "stopwords.txt", "the")

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	// en-US, en-GB, en should all resolve to en.
	for _, tag := range []string{"en", "en-US", "en-GB", "EN"} {
		p := r.Resolve(tag)
		if _, ok := p.StopWords["the"]; !ok {
			t.Errorf("expected 'the' in %s stopwords", tag)
		}
	}
}

func TestLexiconRegistry_FunctionWords(t *testing.T) {
	dir := t.TempDir()
	itDir := filepath.Join(dir, "it")
	writeLexiconFile(t, itDir, "function_words.txt",
		"il", "lo", "la", "di", "a", "da", "che",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	fw := r.FunctionWords("it")
	for _, want := range []string{"il", "la", "che"} {
		if _, ok := fw[want]; !ok {
			t.Errorf("expected function word %q in it", want)
		}
	}
	if _, ok := fw["cane"]; ok {
		t.Errorf("did not expect 'cane' in it function words")
	}
}

func TestLexiconRegistry_VerbSuffixes(t *testing.T) {
	dir := t.TempDir()
	itDir := filepath.Join(dir, "it")
	writeLexiconFile(t, itDir, "verb_morphology.txt",
		"are", "ere", "ire", "ando", "endo",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	suffixes := r.VerbSuffixes("it")
	if len(suffixes) != 5 {
		t.Fatalf("expected 5 suffixes, got %v", suffixes)
	}
	if suffixes[0] != "are" {
		t.Errorf("first suffix = %q, want 'are'", suffixes[0])
	}
}

func TestLexiconRegistry_EntityBlocklist(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "entity_blocklist.txt",
		"ancient", "modern", "great", "new", "old",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	bl := r.EntityBlocklist("en")
	for _, want := range []string{"ancient", "modern", "great"} {
		if _, ok := bl[want]; !ok {
			t.Errorf("expected blocklist entry %q in en", want)
		}
	}
}

func TestLexiconRegistry_PhrasePolicy(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "phrase_policy.txt",
		"min_words=2",
		"max_words=4",
		"max_results=10",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	pp := r.PhrasePolicy("en")
	if pp.MinWords != 2 {
		t.Errorf("MinWords = %d, want 2", pp.MinWords)
	}
	if pp.MaxWords != 4 {
		t.Errorf("MaxWords = %d, want 4", pp.MaxWords)
	}
	if pp.MaxResults != 10 {
		t.Errorf("MaxResults = %d, want 10", pp.MaxResults)
	}
	if !pp.RejectVerbsWhenAll {
		t.Error("expected RejectVerbsWhenAll = true (default)")
	}
}

func TestLexiconRegistry_PhrasePolicyStrictParsing(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "phrase_policy.txt", "min_words=two")

	if _, err := NewLexiconRegistry(dir); err == nil {
		t.Fatal("expected malformed phrase_policy.txt to fail")
	}
}

func TestLexiconRegistry_RequiresFallbackAndLanguageRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewLexiconRegistry(dir); err == nil {
		t.Fatal("expected missing fallback profile to fail")
	}
	if _, err := NewLexiconRegistry(""); err == nil {
		t.Fatal("expected empty root to fail")
	}
	if _, err := NewLexiconRegistry(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected missing root to fail")
	}

	if err := os.MkdirAll(filepath.Join(dir, "en"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "en", "stopwords.txt"), []byte("the\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLexiconRegistry(dir); err == nil {
		t.Fatal("expected fallback requirement to fail before fallback is created")
	}
}

func TestLexiconConfigChangeChangesBehavior(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "stopwords.txt", "before")

	first, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first.StopWords("en")["before"]; !ok {
		t.Fatal("initial configured word was not loaded")
	}

	writeLexiconFile(t, enDir, "stopwords.txt", "after")
	second, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.StopWords("en")["after"]; !ok {
		t.Fatal("updated configured word was not loaded")
	}
	if _, ok := second.StopWords("en")["before"]; ok {
		t.Fatal("stale configured word remained after reload")
	}
}

func TestSetDefaultLexiconRejectsNil(t *testing.T) {
	if err := SetDefaultLexicon(nil); err == nil {
		t.Fatal("expected nil default registry to return an error")
	}
}

func TestDefaultLexiconFailsBeforeBootstrap(t *testing.T) {
	defaultLexiconMu.Lock()
	previous := defaultLexicon
	defaultLexicon = nil
	defaultLexiconMu.Unlock()
	defer func() {
		defaultLexiconMu.Lock()
		defaultLexicon = previous
		defaultLexiconMu.Unlock()
	}()

	defer func() {
		if recover() == nil {
			t.Fatal("expected DefaultLexicon to fail fast")
		}
	}()
	_ = DefaultLexicon()
}

func TestLexiconRegistry_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Create an empty directory structure without fallback.
	os.MkdirAll(filepath.Join(dir, "en"), 0755)

	if _, err := NewLexiconRegistry(dir); err == nil {
		t.Fatal("expected empty root without fallback to fail")
	}
}

func TestLexiconRegistry_CommentsAndEmptyLines(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "stopwords.txt",
		"# English stopwords",
		"the",
		"",
		"and",
		"   ",
		"for",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	sw := r.StopWords("en")
	for _, want := range []string{"the", "and", "for"} {
		if _, ok := sw[want]; !ok {
			t.Errorf("expected stopword %q", want)
		}
	}
	if len(sw) != 3 {
		t.Errorf("expected exactly 3 stopwords, got %d", len(sw))
	}
}

func TestLexiconRegistry_NegativeParticlesAndVisualVerbs(t *testing.T) {
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	writeLexiconFile(t, enDir, "negative_particles.txt",
		"not", "no", "never",
	)
	writeLexiconFile(t, enDir, "visual_verbs.txt",
		"watch", "observe", "look",
	)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	np := r.NegativeParticles("en")
	for _, want := range []string{"not", "no", "never"} {
		if _, ok := np[want]; !ok {
			t.Errorf("expected negative particle %q", want)
		}
	}

	vv := r.VisualVerbs("en")
	for _, want := range []string{"watch", "observe", "look"} {
		if _, ok := vv[want]; !ok {
			t.Errorf("expected visual verb %q", want)
		}
	}
}

func TestLexiconRegistry_HasProfileAndProfileCount(t *testing.T) {
	dir := t.TempDir()
	writeLexiconFile(t, filepath.Join(dir, "en"), "stopwords.txt", "the")

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	if !r.HasProfile("en") {
		t.Error("expected HasProfile(\"en\") = true")
	}
	if !r.HasProfile("en-US") {
		t.Error("expected HasProfile(\"en-US\") = true (region stripped)")
	}
	if r.HasProfile("fr") {
		t.Error("expected HasProfile(\"fr\") = false (only fallback available)")
	}
	if r.ProfileCount() != 2 {
		t.Errorf("expected ProfileCount() = 2 including fallback, got %d", r.ProfileCount())
	}
}

func TestLexiconRegistry_FingerprintIncludesNewFields(t *testing.T) {
	dir := t.TempDir()
	writeLexiconFile(t, filepath.Join(dir, "en"), "stopwords.txt", "the")
	writeLexiconFile(t, filepath.Join(dir, "en"), "negative_particles.txt", "not")
	writeLexiconFile(t, filepath.Join(dir, "en"), "visual_verbs.txt", "watch")

	r1, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}
	v1 := r1.Version()

	// Mutate the visual verbs file and rebuild the registry.
	writeLexiconFile(t, filepath.Join(dir, "en"), "visual_verbs.txt", "watch", "observe")
	r2, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}
	v2 := r2.Version()

	if v1 == v2 {
		t.Error("expected version to change when negative particles / visual verbs change")
	}
}

package linguistics

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLexiconFile(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
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
	// Only create an "it" profile, no fallback.
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

	// French has no profile; should use built-in fallback.
	frProfile := r.Resolve("fr")
	if frProfile == nil {
		t.Fatal("expected non-nil profile for fr (fallback)")
	}
	if _, ok := frProfile.StopWords["the"]; !ok {
		t.Error("expected 'the' in fallback stopwords")
	}
	// French-specific words should NOT be in the fallback.
	if _, ok := frProfile.StopWords["le"]; !ok {
		t.Error("expected 'le' in fallback stopwords")
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

func TestLexiconRegistry_FallbackHasBuiltInData(t *testing.T) {
	// Create registry with NO directories at all.
	dir := t.TempDir()
	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	// German resolves to the built-in fallback (no "de" directory in the empty dir).
	p := r.Resolve("de")
	if p == nil {
		t.Fatal("expected non-nil profile (built-in fallback)")
	}
	if _, ok := p.StopWords["the"]; !ok {
		t.Error("expected built-in fallback to contain 'the'")
	}
	if _, ok := p.StopWords["le"]; !ok {
		t.Error("expected built-in fallback to contain 'le'")
	}
}

func TestDefaultLexicon_FallbackAvailable(t *testing.T) {
	lex := DefaultLexicon()
	if lex == nil {
		t.Fatal("DefaultLexicon() returned nil")
	}

	// German has its own built-in profile with German-specific stop words.
	deProfile := lex.Resolve("de")
	if deProfile == nil {
		t.Fatal("expected non-nil profile for de")
	}
	if _, ok := deProfile.StopWords["der"]; !ok {
		t.Error("expected 'der' in German profile stop words")
	}

	// An unresolvable language (e.g. Japanese) falls back to the union.
	jaProfile := lex.Resolve("ja")
	if jaProfile == nil {
		t.Fatal("expected non-nil profile for ja (fallback)")
	}
	if _, ok := jaProfile.StopWords["the"]; !ok {
		t.Error("expected 'the' in fallback stop words for unresolvable language")
	}
}

func TestLexiconRegistry_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Create an empty directory structure.
	os.MkdirAll(filepath.Join(dir, "en"), 0755)

	r, err := NewLexiconRegistry(dir)
	if err != nil {
		t.Fatalf("NewLexiconRegistry: %v", err)
	}

	p := r.Resolve("en")
	if p == nil {
		t.Fatal("expected non-nil profile for en")
	}
	if len(p.StopWords) != 0 {
		t.Errorf("expected empty stopwords, got %d", len(p.StopWords))
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

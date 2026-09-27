package phrases

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
)

// The entity surface "Ada Lovelace" occupies runes [0,12) of the fixture
// below. Selection takes the blocked RUNE ranges explicitly: entity grounding
// belongs to the caller that owns the entity model, which is what lets this
// package stay a leaf.
var adaSpan = [][2]int{{0, 12}}

func TestSelectImportantPhrasesRanksConfiguredActionsAndSkipsNames(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		StopWords:     map[string]struct{}{"they": {}},
		FunctionWords: map[string]struct{}{"they": {}, "later": {}},
		VisualVerbs:   map[string]struct{}{"built": {}, "fix": {}},
		PhrasePolicy:  linguistics.DefaultPhraseExtractionPolicy(),
	}
	const text = "Ada Lovelace built props. Later, they fix costumes."

	got := Select(text, adaSpan, 5, profile)
	want := []string{"built props", "fix costumes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected phrases = %#v, want %#v", got, want)
	}
	if selected := Select(text, adaSpan, 1, profile); !reflect.DeepEqual(selected, want[:1]) {
		t.Fatalf("limited phrases = %#v, want strongest phrase %#v", selected, want[:1])
	}
}

func TestDeterministicPhraseWordsAreGroundedInSelectedPhrases(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		StopWords:     map[string]struct{}{"the": {}},
		FunctionWords: map[string]struct{}{"the": {}},
		PhrasePolicy:  linguistics.DefaultPhraseExtractionPolicy(),
	}
	selected := Select("The city builds bridges and parks.", nil, 2, profile)
	words := ImportantWordsWithProfile(selected, 3, profile)
	if !reflect.DeepEqual(words, []string{"city", "builds", "bridges"}) {
		t.Fatalf("selected words = %#v, want words grounded in top phrases", words)
	}
}

// TestSelectImportantPhrasesHonoursBlockedSpans pins the neutral-input
// contract. Selection alone has no idea what an entity is — with no blocked
// span it happily ranks the longer "Lovelace built props" — so the caller's
// grounded ranges are the ONLY thing keeping a name out of the phrase surface.
// "Ada Lovelace" occupies runes [0,12) and "built props" [13,24).
func TestSelectImportantPhrasesHonoursBlockedSpans(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		PhrasePolicy: linguistics.DefaultPhraseExtractionPolicy(),
	}
	const text = "Ada Lovelace built props."

	if got := Select(text, nil, 5, profile); !reflect.DeepEqual(got, []string{"Lovelace built props"}) {
		t.Fatalf("ungrounded selection = %#v, want the longer surface", got)
	}
	// Blocking the name span drops the name-prefixed candidate and leaves the
	// clean one — exactly what entity grounding supplies in production.
	if got := Select(text, adaSpan, 5, profile); !reflect.DeepEqual(got, []string{"built props"}) {
		t.Fatalf("name-blocked selection = %#v, want the phrase without the name", got)
	}
	// A span over the action phrase itself drops it entirely.
	if got := Select(text, [][2]int{{13, 24}}, 5, profile); len(got) != 0 {
		t.Fatalf("a phrase overlapping the blocked span must be dropped, got %#v", got)
	}
}

// TestSelectWithCorpusBoostsDocumentRecurrence pins the document term-frequency
// boost: two candidates of equal local strength, but the one that recurs across
// the document corpus must rank first even when source order would prefer the
// other. A nil corpus must preserve Select exactly.
func TestSelectWithCorpusBoostsDocumentRecurrence(t *testing.T) {
	profile := &linguistics.LexiconProfile{PhrasePolicy: linguistics.DefaultPhraseExtractionPolicy()}
	const text = "Gamma delta. Alpha beta."

	if got := Select(text, nil, 5, profile); !reflect.DeepEqual(got, []string{"Gamma delta.", "Alpha beta."}) {
		t.Fatalf("corpus-free selection = %#v, want source order", got)
	}
	if got := SelectWithCorpus(text, nil, 5, profile, nil); !reflect.DeepEqual(got, []string{"Gamma delta.", "Alpha beta."}) {
		t.Fatalf("nil corpus must preserve Select, got %#v", got)
	}

	corpus := []string{text, "Alpha beta."}
	want := []string{"Alpha beta.", "Gamma delta."}
	for run := 0; run < 2; run++ {
		if got := SelectWithCorpus(text, nil, 5, profile, corpus); !reflect.DeepEqual(got, want) {
			t.Fatalf("corpus selection run %d = %#v, want %#v", run, got, want)
		}
	}
}

func TestSelectAllowsNaturalSixWordPhraseWithInternalFunctionWords(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		StopWords:     map[string]struct{}{"o": {}, "da": {}},
		FunctionWords: map[string]struct{}{"o": {}, "da": {}},
		PhrasePolicy:  linguistics.PhraseExtractionPolicy{MinWords: 2, MaxWords: 6, MaxResults: 5},
	}
	const phrase = "O maior arrependimento da minha vida"
	got := Select(phrase, nil, 5, profile)
	if len(got) == 0 || got[0] != phrase {
		t.Fatalf("selected phrases = %#v, want the complete natural six-word phrase %q", got, phrase)
	}
}

func TestSelectUsesCheckedInPortuguesePhrasePolicyAndFunctionWords(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate selection test source")
	}
	lexicons := filepath.Clean(filepath.Join(filepath.Dir(source), "../../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(lexicons)
	if err != nil {
		t.Fatalf("load checked-in lexicons: %v", err)
	}
	profile := registry.Resolve("pt-BR")
	if profile.PhrasePolicy.MaxWords != 6 {
		t.Fatalf("checked-in PT-BR max words = %d, want default six-word policy", profile.PhrasePolicy.MaxWords)
	}
	const phrase = "O maior arrependimento da minha vida"
	got := Select(phrase, nil, 5, profile)
	if len(got) == 0 || got[0] != phrase {
		t.Fatalf("selected PT-BR phrases = %#v, want the complete grounded phrase %q", got, phrase)
	}

	// The same internal function words must still be rejected when they
	// exceed one third of candidate words, even under the loaded PT-BR
	// function-word set.
	tooDense := "O da de para maior arrependimento vida"
	profile.PhrasePolicy.MinWords = 6
	profile.PhrasePolicy.MaxWords = 6
	if selected := Select(tooDense, nil, 5, profile); len(selected) != 0 {
		t.Fatalf("PT-BR phrase with excessive function words was accepted: %#v", selected)
	}
}

func TestSelectHardCapsPhraseCandidatesAtSixWords(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		PhrasePolicy: linguistics.PhraseExtractionPolicy{MinWords: 2, MaxWords: 9, MaxResults: 5},
	}
	selected := Select("alpha beta gamma delta epsilon zeta eta theta", nil, 5, profile)
	for _, phrase := range selected {
		if words := len(strings.Fields(phrase)); words > 6 {
			t.Fatalf("hard-capped selection returned %d-word phrase %q", words, phrase)
		}
	}
	if len(selected) == 0 {
		t.Fatal("expected at least one phrase under the six-word cap")
	}
}

func TestSelectIncludesCompleteSentenceAndTwoSentenceCardWithoutOverlap(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		PhrasePolicy: linguistics.PhraseExtractionPolicy{MinWords: 2, MaxWords: 6, MaxResults: 5},
	}
	const text = "We built a garden for the neighbors. The garden brought people together. Everyone found a place."
	got := Select(text, nil, 5, profile)
	want := []string{
		"We built a garden for the neighbors. The garden brought people together.",
		"Everyone found a place.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sentence-card selection = %#v, want two consecutive sentences followed by the disjoint third", got)
	}
	firstStart := strings.Index(text, got[0])
	firstEnd := firstStart + len(got[0])
	secondStart := strings.Index(text, got[1])
	if firstStart < 0 || secondStart < firstEnd {
		t.Fatalf("selected sentence cards overlap or are not source-ordered: %#v", got)
	}
}

func TestSelectPreservesSingleCompleteSentenceBeyondSixWords(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		PhrasePolicy: linguistics.PhraseExtractionPolicy{MinWords: 2, MaxWords: 6, MaxResults: 5},
	}
	const sentence = "The remarkable discovery changed how researchers understand ancient life."
	got := Select(sentence, nil, 5, profile)
	if len(got) == 0 || got[0] != sentence {
		t.Fatalf("complete sentence selection = %#v, want %q", got, sentence)
	}
	if words := len(strings.Fields(got[0])); words <= 6 {
		t.Fatalf("fixture has %d words, want a sentence beyond the fragment cap", words)
	}
}

func TestSelectRejectsFunctionWordDensityAboveOneThird(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		StopWords:     map[string]struct{}{"of": {}, "my": {}, "in": {}},
		FunctionWords: map[string]struct{}{"of": {}, "my": {}, "in": {}},
		PhrasePolicy:  linguistics.PhraseExtractionPolicy{MinWords: 6, MaxWords: 6, MaxResults: 5},
	}
	const phrase = "greatest of my in entire life"
	if got := Select(phrase, nil, 5, profile); len(got) != 0 {
		t.Fatalf("phrase with half function words should be rejected, got %#v", got)
	}
}

func TestContainsProperNamePairDetectsConsecutiveCapitalisedWords(t *testing.T) {
	for input, want := range map[string]bool{
		"Ada Lovelace":     true,
		"built props":      false,
		"the Dolly Parton": true,
		"fix costumes":     false,
		"":                 false,
	} {
		if got := ContainsProperNamePair(input); got != want {
			t.Fatalf("ContainsProperNamePair(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestContainsProperNamePairIgnoresAcronymRuns pins the all-caps rule: a word
// that is entirely uppercase with two or more letters is an acronym, not a
// proper-name trigger. A single capital stays an ordinary capitalised word, so
// "John F Kennedy" is still a name run.
func TestContainsProperNamePairIgnoresAcronymRuns(t *testing.T) {
	for input, want := range map[string]bool{
		"USA NATO":       false,
		"AI ML models":   false,
		"the AI act":     false,
		"IBM GmbH":       false,
		"John F Kennedy": true,
		"New York":       true,
	} {
		if got := ContainsProperNamePair(input); got != want {
			t.Fatalf("ContainsProperNamePair(%q) = %v, want %v", input, got, want)
		}
	}
}

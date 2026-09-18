package phrases

import (
	"reflect"
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
// contract: a phrase whose runes overlap a blocked span is never selected, so
// a caller that grounds its own entities keeps a name out of the phrase
// surface without this package knowing what an entity is. "built props"
// occupies runes [13,24) of the fixture, immediately after the name.
func TestSelectImportantPhrasesHonoursBlockedSpans(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		PhrasePolicy: linguistics.DefaultPhraseExtractionPolicy(),
	}
	const text = "Ada Lovelace built props."

	if got := Select(text, nil, 5, profile); len(got) == 0 {
		t.Fatalf("without a blocked span the action phrase must survive, got %#v", got)
	}
	if got := Select(text, [][2]int{{13, 24}}, 5, profile); len(got) != 0 {
		t.Fatalf("a phrase overlapping the blocked span must be dropped, got %#v", got)
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

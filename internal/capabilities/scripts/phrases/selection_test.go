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

package scriptgeneration

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSelectImportantPhrasesRanksConfiguredActionsAndSkipsNames(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		StopWords:     map[string]struct{}{"they": {}},
		FunctionWords: map[string]struct{}{"they": {}, "later": {}},
		VisualVerbs:   map[string]struct{}{"built": {}, "fix": {}},
		PhrasePolicy:  linguistics.DefaultPhraseExtractionPolicy(),
	}
	const text = "Ada Lovelace built props. Later, they fix costumes."
	entities := []VisualEntity{{Text: "Ada Lovelace", Type: scriptpkg.EntityTypePerson}}

	got := selectImportantPhrases(text, entities, 5, profile)
	want := []string{"built props", "fix costumes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected phrases = %#v, want %#v", got, want)
	}
	if selected := selectImportantPhrases(text, entities, 1, profile); !reflect.DeepEqual(selected, want[:1]) {
		t.Fatalf("limited phrases = %#v, want strongest phrase %#v", selected, want[:1])
	}
}

func TestDeterministicPhraseWordsAreGroundedInSelectedPhrases(t *testing.T) {
	profile := &linguistics.LexiconProfile{
		StopWords:     map[string]struct{}{"the": {}},
		FunctionWords: map[string]struct{}{"the": {}},
		PhrasePolicy:  linguistics.DefaultPhraseExtractionPolicy(),
	}
	phrases := selectImportantPhrases("The city builds bridges and parks.", nil, 2, profile)
	words := deterministicImportantWordsWithProfile(phrases, 3, profile)
	if !reflect.DeepEqual(words, []string{"city", "builds", "bridges"}) {
		t.Fatalf("selected words = %#v, want words grounded in top phrases", words)
	}
}

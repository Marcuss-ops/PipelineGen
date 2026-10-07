package runner

import (
	"reflect"
	"testing"
)

func TestLanguageDispatchContracts(t *testing.T) {
	if got := LanguagePriority("en", []string{"es", "it"}, "fr"); got != 4 {
		t.Fatalf("undeclared language priority = %d, want 4", got)
	}
	got := OrderedLanguages(map[string]string{"it": "ciao", "es": "hola", "en": "hello", "fr": "bonjour"}, "en", []string{"es", "it"})
	if want := []string{"en", "es", "it", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedLanguages() = %v, want %v", got, want)
	}
	if got := RequestedLanguages("en", []string{"es", "en", "", "it"}); !reflect.DeepEqual(got, []string{"en", "es", "it"}) {
		t.Fatalf("RequestedLanguages() = %v", got)
	}
}

func TestBuildVoiceoverLanguageWorkPreservesExplicitEmptySemantics(t *testing.T) {
	text := map[string]string{"fr": "bonjour", "en": "hello", "es": "hola", "de": ""}
	all := BuildVoiceoverLanguageWork(text, "en", []string{"es"}, nil)
	if got, want := RequestedVoiceoverLanguages(text, "en", []string{"es"}, nil), []string{"en", "es", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("omitted voiceover filter = %v, want %v (all=%d)", got, want, len(all))
	}
	none := BuildVoiceoverLanguageWork(text, "en", []string{"es"}, []string{})
	if len(none) != 0 {
		t.Fatalf("explicit empty filter selected work: %+v", none)
	}
	selected := BuildVoiceoverLanguageWork(text, "en", []string{"es"}, []string{"fr", "en"})
	if got, want := RequestedVoiceoverLanguages(text, "en", []string{"es"}, []string{"fr", "en"}), []string{"en", "fr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit selection = %v, want %v (work=%+v)", got, want, selected)
	}
}

func TestSortBySceneAndLanguageKeepsStableEqualKeys(t *testing.T) {
	type task struct {
		scene int
		lang  string
		id    string
	}
	work := []task{{1, "it", "b"}, {0, "it", "it"}, {0, "es", "es"}, {0, "es", "es-2"}, {0, "en", "en"}}
	SortBySceneAndLanguage(work, func(v task) int { return v.scene }, func(v task) string { return v.lang }, "en", []string{"es", "it"})
	got := []string{work[0].id, work[1].id, work[2].id, work[3].id, work[4].id}
	want := []string{"en", "es", "es-2", "it", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stable work order = %v, want %v", got, want)
	}
}

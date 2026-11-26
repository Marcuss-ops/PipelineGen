package script

import "testing"

func TestFindEditorialDirectivesFindsTheLeakedStageDirections(t *testing.T) {
	cases := []string{
		"Ricostruisci Francesco Totti a partire dalle clip del 2026.",
		"Apri con tre stacchi rapidi sulla maglia.",
		"Non leggere gli URL ad alta voce.",
		"Open with a tight shot of the shirt.",
	}
	for _, text := range cases {
		directives := FindEditorialDirectives(text)
		if len(directives) == 0 {
			t.Fatalf("FindEditorialDirectives(%q) found nothing", text)
		}
		if directives[0].Offset != 0 {
			t.Fatalf("offset = %d, want 0", directives[0].Offset)
		}
		if !HasEditorialDirectives(text) {
			t.Fatalf("HasEditorialDirectives(%q) = false", text)
		}
	}
}

func TestFindEditorialDirectivesKeepsNarrationQuiet(t *testing.T) {
	cases := []string{
		"Ilary mostra la maglia e usa la stessa frase.",
		"La scena descrive il matrimonio di settembre.",
		"Totti non legge il comunicato, lo commenta.",
		"Mostra il volto della coppia al pubblico.",
	}
	for _, text := range cases {
		if directives := FindEditorialDirectives(text); len(directives) != 0 {
			t.Fatalf("FindEditorialDirectives(%q) = %+v, want none", text, directives)
		}
	}
}

func TestFindEditorialDirectivesReportsInnerSentenceDirectives(t *testing.T) {
	text := "La clip documenta le accuse. Poi scrivi un titolo breve. Fine."
	directives := FindEditorialDirectives(text)
	if len(directives) != 1 {
		t.Fatalf("directives = %+v, want one", directives)
	}
	if directives[0].Phrase != "scrivi un titolo breve" {
		t.Fatalf("phrase = %q", directives[0].Phrase)
	}
	if directives[0].Offset <= 0 {
		t.Fatalf("offset = %d, want an inner offset", directives[0].Offset)
	}
}

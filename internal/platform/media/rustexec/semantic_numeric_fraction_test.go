package rustexec

import (
	"strings"
	"testing"
)

func numericFactAt(source, surface, kind string) visualNumericFact {
	start := strings.Index(source, surface)
	return visualNumericFact{
		Surface:   surface,
		Kind:      kind,
		StartByte: start,
		EndByte:   start + len(surface),
		Evidence:  surface,
	}
}

func TestValidateNumericFactsAcceptsItalianFractions(t *testing.T) {
	source := "un terzo, due terzi, metà e tre quarti"
	for _, surface := range []string{"un terzo", "due terzi", "metà", "tre quarti"} {
		fact := numericFactAt(source, surface, "FRACTION")
		if fact.StartByte < 0 {
			t.Fatalf("surface %q not found in fixture", surface)
		}
		if err := validateNumericFacts(source, []visualNumericFact{fact}); err != nil {
			t.Fatalf("FRACTION %q rejected: %v", surface, err)
		}
	}
}

func TestValidateNumericFactsStillRejectsUnknownKinds(t *testing.T) {
	source := "un terzo"
	fact := numericFactAt(source, source, "RATIO")
	if err := validateNumericFacts(source, []visualNumericFact{fact}); err == nil {
		t.Fatal("an unknown numeric kind must still fail explicitly")
	}
}

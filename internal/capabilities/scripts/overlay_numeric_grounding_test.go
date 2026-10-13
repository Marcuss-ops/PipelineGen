package scriptgeneration

import "testing"

func TestIsNumericOverlayValueRequiresWholeValueToBeNumeric(t *testing.T) {
	for _, value := range []string{"13 agosto 2007", "data 13 agosto 2007", "1.2 million", "18 ottobre 2026"} {
		if !isNumericOverlayValue(value) {
			t.Errorf("isNumericOverlayValue(%q) = false, want true", value)
		}
	}
	for _, value := range []string{
		"partendo dal 13 agosto 2007",
		"discussione tecnica riguardante l'impronta identificata come impronta 33 si",
		"infine l'8 ottobre 2026",
	} {
		if isNumericOverlayValue(value) {
			t.Errorf("isNumericOverlayValue(%q) = true, want false", value)
		}
	}
}

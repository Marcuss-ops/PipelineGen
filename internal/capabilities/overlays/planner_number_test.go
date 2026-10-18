package overlays

import "testing"

func TestDateCardWindowCapShortensOnlyWhenNextSpokenDateWouldOverlap(t *testing.T) {
	first := TimedAnnotation{Text: "13 agosto 2007", Type: "DATE", StartUS: 1_000_000, DurationUS: 1_000_000}
	second := TimedAnnotation{Text: "2015", Type: "DATE", StartUS: 3_500_000, DurationUS: 750_000}
	third := TimedAnnotation{Text: "28 settembre 2026", Type: "DATE", StartUS: 9_000_000, DurationUS: 1_500_000}
	all := []TimedAnnotation{third, first, second}
	if got := dateCardWindowCap(first, all); got != 2_400_000 {
		t.Fatalf("first date cap = %d, want 2.4s before the next date", got)
	}
	if got := dateCardWindowCap(second, all); got != 0 {
		t.Fatalf("second date cap = %d, want no cap for a non-overlapping following date", got)
	}
	if got := numberOverlayItem("plan", "scene", first, nil).DurationUS; got != 5_000_000 {
		t.Fatalf("single date duration = %d, want the default 5s", got)
	}
	item := numberOverlayItem("plan", "scene", first, nil, dateCardWindowCap(first, all))
	if item.DurationUS != 2_400_000 {
		t.Fatalf("first date duration = %d, want 2.4s", item.DurationUS)
	}
}

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

func TestDateCardWindowCapStopsBeforeFollowingImage(t *testing.T) {
	date := TimedAnnotation{Text: "17 dicembre 2009", Type: "DATE", StartUS: 44_033_000, DurationUS: 1_000_000}
	images := []ImageCandidate{{StartUS: 46_582_000, DurationUS: 1_738_000}}
	if got := dateCardWindowCapBeforeImage(date, images, 0); got != 2_449_000 {
		t.Fatalf("date/image overlap cap = %d, want 2.449s", got)
	}
	item := numberOverlayItem("plan", "scene", date, nil, dateCardWindowCapBeforeImage(date, images, 0))
	if item.EndMs >= 46_582 {
		t.Fatalf("date card ends at %dms beneath image starting at 46582ms", item.EndMs)
	}
}

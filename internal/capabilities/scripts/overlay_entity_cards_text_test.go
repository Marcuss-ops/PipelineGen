package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestDedupeOverlappingGroundedDatesKeepsCompleteSurface(t *testing.T) {
	items := []capabilityoverlay.TimedAnnotation{
		{Text: "17 dicembre", Type: "DATE", StartUS: 44_033_000, DurationUS: 1_200_000},
		{Text: "dicembre 2009", Type: "DATE", StartUS: 44_033_000, DurationUS: 1_200_000},
		{Text: "17 dicembre 2009", Type: "DATE", StartUS: 44_033_000, DurationUS: 1_200_000},
		{Text: "6 dicembre 2011", Type: "DATE", StartUS: 68_322_000, DurationUS: 1_100_000},
	}
	got := dedupeOverlappingGroundedDates(items)
	if len(got) != 2 || got[0].Text != "17 dicembre 2009" || got[1].Text != "6 dicembre 2011" {
		t.Fatalf("consolidated dates = %#v, want one complete card per spoken date", got)
	}
}

func TestDedupeOverlappingGroundedDatesLeavesNonDatesAndSeparateMentions(t *testing.T) {
	items := []capabilityoverlay.TimedAnnotation{
		{Text: "17 dicembre", Type: "DATE", StartUS: 10_000_000, DurationUS: 900_000},
		{Text: "18 dicembre", Type: "DATE", StartUS: 12_000_000, DurationUS: 900_000},
		{Text: "sedici anni", Type: "NUMBER", StartUS: 10_000_000, DurationUS: 900_000},
	}
	if got := dedupeOverlappingGroundedDates(items); len(got) != len(items) {
		t.Fatalf("distinct dates or non-date number were removed: %#v", got)
	}
}

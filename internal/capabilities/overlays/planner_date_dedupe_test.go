package overlays

import "testing"

func TestDedupeCoTimedPartialDatesKeepsFullSurface(t *testing.T) {
	full := TimedAnnotation{Text: "3 giugno 2007", Type: "DATE", StartUS: 1_607_375, DurationUS: 1_460_950, Score: 1}
	day := TimedAnnotation{Text: "3 giugno", Type: "DATE", StartUS: 1_607_375, DurationUS: 1_460_950, Score: 1}
	month := TimedAnnotation{Text: "giugno 2007", Type: "DATE", StartUS: 1_607_375, DurationUS: 1_460_950, Score: 1}
	other := TimedAnnotation{Text: "14 ottobre 2010", Type: "DATE", StartUS: 4_897_075, DurationUS: 2_275_000, Score: 1}

	for _, input := range [][]TimedAnnotation{
		{full, day, month},
		{day, month, full},
		{month, day, full},
	} {
		got := dedupeCoTimedDateAnnotations(input)
		if len(got) != 1 || got[0].Text != full.Text {
			t.Fatalf("dedupe(%v) = %+v, want only the full date %q", input, got, full.Text)
		}
	}

	got := dedupeCoTimedDateAnnotations([]TimedAnnotation{full, day, month, other})
	if len(got) != 2 || got[0].Text != full.Text || got[1].Text != other.Text {
		t.Fatalf("dedupe with two distinct dates = %+v, want the two full dates", got)
	}
}

func TestDateSurfacesOverlapRejectsDistinctDates(t *testing.T) {
	if dateSurfacesOverlap("3 giugno 2007", "14 ottobre 2010") {
		t.Fatal("distinct full dates must not be treated as duplicates")
	}
	if dateSurfacesOverlap("3 giugno", "14 ottobre") {
		t.Fatal("distinct partial dates must not be treated as duplicates")
	}
	if !dateSurfacesOverlap("3 giugno 2007", "3 giugno") {
		t.Fatal("partial day surface must match its full date")
	}
	if !dateSurfacesOverlap("3 giugno 2007", "giugno 2007") {
		t.Fatal("partial month/year surface must match its full date")
	}
}

func TestDateSceneEndCapDropsUnreadableLastCard(t *testing.T) {
	last := TimedAnnotation{Text: "9 novembre 2019", Type: "DATE", StartUS: 12_591_825, DurationUS: 1_875_000}
	if _, ok := dateSceneEndCap(last, 14_472_000); ok {
		t.Fatal("a date with less than two readable seconds before the scene end must be dropped")
	}
	earlier := TimedAnnotation{Text: "22 aprile 2014", Type: "DATE", StartUS: 10_000_000, DurationUS: 1_500_000}
	if cap, ok := dateSceneEndCap(earlier, 14_472_000); !ok || cap != 4_472_000 {
		t.Fatalf("scene cap = %d/%t, want 4.472s readable", cap, ok)
	}
}

func TestDateSurfacesOverlapKeepsSameYearDistinctDates(t *testing.T) {
	if dateSurfacesOverlap("3 giugno 2007", "4 giugno 2007") {
		t.Fatal("two different days in the same year must not be treated as duplicates")
	}
	if !dateSurfacesOverlap("3 giugno 2007", "giugno 2007") {
		t.Fatal("a month/year partial must still match its full date")
	}
	if !dateSurfacesOverlap("3 giugno 2007", "3 giugno 2007") {
		t.Fatal("identical surfaces must still deduplicate")
	}
}

func TestDedupeKeepsDistinctDatesAtSameStart(t *testing.T) {
	input := []TimedAnnotation{
		{Text: "3 giugno 2007", Type: "DATE", StartUS: 1_000_000, DurationUS: 500_000, Score: 2},
		{Text: "4 giugno 2007", Type: "DATE", StartUS: 1_000_000, DurationUS: 500_000, Score: 1},
	}
	got := dedupeCoTimedDateAnnotations(input)
	if len(got) != 2 || got[0].Text != "3 giugno 2007" || got[1].Text != "4 giugno 2007" {
		t.Fatalf("same-start distinct dates = %+v, want both dates", got)
	}
}

func TestBuildPlanKeepsMetricNearSceneEnd(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "scene-end-cap", VideoID: "video", Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1", EndUS: 10_000_000,
			Numbers: []TimedAnnotation{
				{Text: "9 novembre 2019", Type: "DATE", StartMs: 9_000, EndMs: 9_500, StartUS: 9_000_000, DurationUS: 500_000, Score: 2},
				{Text: "42", Type: "NUMBER", StartMs: 9_000, EndMs: 9_500, StartUS: 9_000_000, DurationUS: 500_000, Score: 1},
			},
		}},
	}, PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.Text == "42" && item.TemplateID == "METRIC_STAT_CARD" {
			return
		}
	}
	t.Fatalf("metric near the scene end was dropped with the date lane: %+v", plan.Items)
}

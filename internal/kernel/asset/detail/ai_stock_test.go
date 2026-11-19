package detail

import "testing"

func TestVisualAnalysisSortedEventsDoesNotMutateInput(t *testing.T) {
	a := VisualAnalysis{Events: []VisualEvent{{StartMs: 20, EndMs: 30}, {StartMs: 0, EndMs: 10}}}
	got := a.SortedEvents()
	if got[0].StartMs != 0 || a.Events[0].StartMs != 20 {
		t.Fatalf("sorting mutated input: %#v", a.Events)
	}
}

package asset

import "testing"

func TestAIStockMetadataContract(t *testing.T) {
	m := AIStockMetadata{AssetRole: AssetRoleStock, NormalizedGroup: NormalizedGroupStock, FolderPath: AIStockFolderPath("Boxe"), AudioProfile: AudioProfileAmbientAndEffects}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := AIStockFolderPath(""); got != "Stock/AI/Generico" {
		t.Fatalf("folder=%q", got)
	}
}

func TestVisualAnalysisRejectsUnorderedOrInvalidEvents(t *testing.T) {
	a := VisualAnalysis{Events: []VisualEvent{{StartMs: 100, EndMs: 200, Text: "ok"}, {StartMs: 150, EndMs: 300, Text: "overlap"}}}
	if err := a.Validate(1000); err == nil {
		t.Fatal("expected invalid visual track")
	}
}

func TestVisualAnalysisSortedEventsDoesNotMutateInput(t *testing.T) {
	a := VisualAnalysis{Events: []VisualEvent{{StartMs: 20, EndMs: 30}, {StartMs: 0, EndMs: 10}}}
	got := a.SortedEvents()
	if got[0].StartMs != 0 || a.Events[0].StartMs != 20 {
		t.Fatalf("sorting mutated input: %#v", a.Events)
	}
}

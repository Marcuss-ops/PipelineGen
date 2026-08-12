package mediaregistry

import "testing"

func TestValidateCountsRejectsEnrichmentLoss(t *testing.T) {
	err := ValidateCounts(Counts{Assets: 631, Transcripts: 590, Descriptions: 587}, Counts{Assets: 631}, false)
	if err == nil {
		t.Fatal("expected invariant failure when enrichment disappears")
	}
}

func TestValidateCountsAllowsGrowth(t *testing.T) {
	if err := ValidateCounts(Counts{Assets: 1, Transcripts: 0}, Counts{Assets: 2, Transcripts: 1}, false); err != nil {
		t.Fatalf("unexpected invariant failure: %v", err)
	}
}

func TestValidateCountsAllowsExplicitDestructiveRun(t *testing.T) {
	if err := ValidateCounts(Counts{Assets: 10, Transcripts: 10}, Counts{}, true); err != nil {
		t.Fatalf("unexpected destructive-run failure: %v", err)
	}
}

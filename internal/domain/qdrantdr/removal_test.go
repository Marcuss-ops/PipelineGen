package qdrantdr

import (
	"strings"
	"testing"
	"time"
)

func completeRemovalEvidence() LegacyRemovalEvidence {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return LegacyRemovalEvidence{
		CompleteZeroScans:      3,
		RequiredZeroScans:      3,
		QdrantAllowlistEntries: 0,
		ObservationStartedAt:   start,
		ObservationNow:         start.Add(30 * 24 * time.Hour),
		RequiredObservation:    30 * 24 * time.Hour,
	}
}

func TestEvaluateLegacyRemoval_EligibleOnlyWithAllEvidence(t *testing.T) {
	decision := EvaluateLegacyRemoval(completeRemovalEvidence())
	if !decision.Eligible || len(decision.Reasons) != 0 {
		t.Fatalf("expected eligible decision, got %+v", decision)
	}
}

func TestEvaluateLegacyRemoval_FailsClosedOnAnyResidual(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*LegacyRemovalEvidence)
	}{
		{"incomplete scans", func(e *LegacyRemovalEvidence) { e.CompleteZeroScans = 2 }},
		{"pending cleanup", func(e *LegacyRemovalEvidence) { e.PendingCleanup = 1 }},
		{"allowlist", func(e *LegacyRemovalEvidence) { e.QdrantAllowlistEntries = 1 }},
		{"legacy tests", func(e *LegacyRemovalEvidence) { e.LegacyProductionTests = 1 }},
		{"reappearance", func(e *LegacyRemovalEvidence) { e.ReappearanceCount = 1 }},
		{"short window", func(e *LegacyRemovalEvidence) { e.ObservationNow = e.ObservationStartedAt.Add(29 * 24 * time.Hour) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence := completeRemovalEvidence()
			tc.mutate(&evidence)
			decision := EvaluateLegacyRemoval(evidence)
			if decision.Eligible {
				t.Fatalf("expected ineligible decision, got %+v", decision)
			}
			if len(decision.Reasons) == 0 || strings.TrimSpace(decision.Reasons[0]) == "" {
				t.Fatalf("expected an audit reason, got %+v", decision)
			}
		})
	}
}

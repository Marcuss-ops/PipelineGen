package dr

import "time"

// LegacyRemovalEvidence is the measured evidence required before removing
// the Qdrant legacy-audit classifier, cleanup path, duplicate-type allowlist,
// and their dedicated regression tests. A zero result from one scan is not
// sufficient: the evidence must cover complete scans and an observation
// window so a delayed reappearance cannot be mistaken for cleanup.
type LegacyRemovalEvidence struct {
	// CompleteZeroScans is the number of consecutive complete audit scans
	// with zero findings in every legacy category.
	CompleteZeroScans int
	// RequiredZeroScans is the policy threshold for CompleteZeroScans.
	RequiredZeroScans int
	// PendingCleanup is the count of cleanup/outbox actions still pending.
	PendingCleanup int
	// QdrantAllowlistEntries is the number of active Qdrant duplicate-type
	// allowlist rows. Other architecture allowlists are intentionally excluded.
	QdrantAllowlistEntries int
	// LegacyProductionTests is the count of production legacy regression
	// tests that still exercise the retired surface. Ordinary current-contract
	// tests are not counted.
	LegacyProductionTests int
	// ReappearanceCount counts legacy findings observed after the last cleanup
	// run during the observation window.
	ReappearanceCount    int
	ObservationStartedAt time.Time
	ObservationNow       time.Time
	RequiredObservation  time.Duration
}

// LegacyRemovalDecision is the auditable result of the retirement gate.
type LegacyRemovalDecision struct {
	Eligible bool
	Reasons  []string
}

// EvaluateLegacyRemoval returns Eligible only when every measured condition
// is satisfied. Missing or malformed evidence fails closed.
func EvaluateLegacyRemoval(e LegacyRemovalEvidence) LegacyRemovalDecision {
	decision := LegacyRemovalDecision{Eligible: true}
	require := func(ok bool, reason string) {
		if !ok {
			decision.Eligible = false
			decision.Reasons = append(decision.Reasons, reason)
		}
	}

	require(e.RequiredZeroScans > 0, "required zero-scan threshold must be positive")
	require(e.CompleteZeroScans >= e.RequiredZeroScans, "consecutive complete zero-finding scans below threshold")
	require(e.PendingCleanup == 0, "pending cleanup/outbox actions remain")
	require(e.QdrantAllowlistEntries == 0, "Qdrant duplicate-type allowlist entries remain")
	require(e.LegacyProductionTests == 0, "legacy production regression tests remain")
	require(e.ReappearanceCount == 0, "legacy findings reappeared during observation window")
	require(!e.ObservationStartedAt.IsZero(), "observation start is missing")
	require(!e.ObservationNow.Before(e.ObservationStartedAt), "observation clock is invalid")
	require(e.RequiredObservation > 0, "required observation window must be positive")
	if !e.ObservationStartedAt.IsZero() && !e.ObservationNow.Before(e.ObservationStartedAt) {
		require(e.ObservationNow.Sub(e.ObservationStartedAt) >= e.RequiredObservation, "observation window is incomplete")
	}
	return decision
}

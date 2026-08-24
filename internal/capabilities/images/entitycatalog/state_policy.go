package images

import (
	"errors"
	"strings"
	"time"
)

// Candidate freshness policy. The clock is evaluated against LastSeenAt,
// which is updated only when the catalog accepts a provider result or a
// successful materialization verification. Materialization.LastVerifiedAt is
// a separate Drive/local-asset guarantee and does not make a broken remote URL
// usable again.
const (
	// CandidateFreshAfter is the maximum age of a recently observed candidate.
	// At exactly this boundary the candidate is still fresh; it becomes stale
	// only when age > CandidateFreshAfter.
	CandidateFreshAfter = 7 * 24 * time.Hour
)

var ErrInvalidCandidateTransition = errors.New("entity image catalog: invalid candidate state transition")

// CandidateStateEvent is a bounded event in the catalog state machine.
type CandidateStateEvent string

const (
	CandidateEventProviderAccepted    CandidateStateEvent = "provider_accepted"
	CandidateEventValidationSucceeded CandidateStateEvent = "validation_succeeded"
	CandidateEventValidationFailed    CandidateStateEvent = "validation_failed"
	CandidateEventManualRetirement    CandidateStateEvent = "manual_retirement"
)

// CandidateStateAssessment is the deterministic result of evaluating one
// candidate at a point in time. Usable includes fresh and stale; broken and
// retired are never usable for normal discovery.
type CandidateStateAssessment struct {
	State              string
	Usable             bool
	RefreshRecommended bool
}

// ClassifyCandidateStatus applies the canonical time policy without mutating
// persistence. It intentionally preserves broken and retired as explicit
// outcomes: age alone never converts a candidate into broken.
//
// Legacy rows with no LastSeenAt retain their stored state. This prevents a
// migration or a partial fixture from inventing an age while still allowing
// rows with a real timestamp to decay deterministically.
func ClassifyCandidateStatus(now time.Time, candidate Candidate) string {
	stored := strings.ToLower(strings.TrimSpace(candidate.Status))
	switch stored {
	case CandidateStatusBroken, CandidateStatusRetired:
		return stored
	case CandidateStatusActive:
		stored = CandidateStatusFresh
	case CandidateStatusFresh, CandidateStatusStale:
		// keep the normalized stored state until the timestamp check below
	default:
		stored = CandidateStatusFresh
	}
	if candidate.LastSeenAt.IsZero() {
		return stored
	}
	age := now.UTC().Sub(candidate.LastSeenAt.UTC())
	if age <= CandidateFreshAfter {
		return CandidateStatusFresh
	}
	return CandidateStatusStale
}

// AssessCandidateState returns the state and the operational action implied by
// the policy. Stale candidates remain eligible as fallback; refresh is
// recommended only when the caller's usable-pool policy says more candidates
// are needed. The assessment itself therefore never forces a provider call.
func AssessCandidateState(now time.Time, candidate Candidate) CandidateStateAssessment {
	state := ClassifyCandidateStatus(now, candidate)
	usable := IsUsableCandidateStatus(state)
	return CandidateStateAssessment{
		State:              state,
		Usable:             usable,
		RefreshRecommended: state == CandidateStatusStale,
	}
}

// TransitionCandidateStatus applies an explicit state-machine event. Clock
// ageing is not an event: callers use ClassifyCandidateStatus and persist the
// resulting stale state when they observe it.
func TransitionCandidateStatus(current string, event CandidateStateEvent) (string, error) {
	current = strings.ToLower(strings.TrimSpace(current))
	if current == CandidateStatusActive {
		current = CandidateStatusFresh
	}
	if err := ValidateCandidateStatus(current); err != nil {
		return "", err
	}
	if current == CandidateStatusRetired {
		return CandidateStatusRetired, nil
	}

	switch event {
	case CandidateEventProviderAccepted, CandidateEventValidationSucceeded:
		return CandidateStatusFresh, nil
	case CandidateEventValidationFailed:
		return CandidateStatusBroken, nil
	case CandidateEventManualRetirement:
		return CandidateStatusRetired, nil
	default:
		return "", ErrInvalidCandidateTransition
	}
}

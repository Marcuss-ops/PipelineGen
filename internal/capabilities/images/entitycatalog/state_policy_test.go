package images

import (
	"testing"
	"time"
)

func TestClassifyCandidateStatusFreshBoundaryAndStaleTransition(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	candidate := Candidate{
		Status:     CandidateStatusFresh,
		LastSeenAt: now.Add(-CandidateFreshAfter),
	}
	if got := ClassifyCandidateStatus(now, candidate); got != CandidateStatusFresh {
		t.Fatalf("at fresh boundary = %q, want %q", got, CandidateStatusFresh)
	}
	candidate.LastSeenAt = now.Add(-CandidateFreshAfter - time.Nanosecond)
	assessment := AssessCandidateState(now, candidate)
	if assessment.State != CandidateStatusStale || !assessment.Usable || !assessment.RefreshRecommended {
		t.Fatalf("after fresh boundary = %+v, want stale usable refresh-recommended", assessment)
	}
}

func TestClassifyCandidateStatusPreservesBrokenAndRetired(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, status := range []string{CandidateStatusBroken, CandidateStatusRetired} {
		candidate := Candidate{Status: status, LastSeenAt: now.Add(-365 * 24 * time.Hour)}
		assessment := AssessCandidateState(now, candidate)
		if assessment.State != status || assessment.Usable || assessment.RefreshRecommended {
			t.Fatalf("%s assessment = %+v, want non-usable terminal state", status, assessment)
		}
	}
}

func TestClassifyCandidateStatusLegacyWithoutTimestampDoesNotInventStaleness(t *testing.T) {
	assessment := AssessCandidateState(time.Now().UTC(), Candidate{
		Status: CandidateStatusActive,
	})
	if assessment.State != CandidateStatusFresh || !assessment.Usable || assessment.RefreshRecommended {
		t.Fatalf("legacy assessment = %+v, want fresh usable without invented refresh", assessment)
	}
}

func TestTransitionCandidateStatusEvents(t *testing.T) {
	cases := []struct {
		name  string
		from  string
		event CandidateStateEvent
		want  string
	}{
		{name: "provider populates new candidate", from: CandidateStatusFresh, event: CandidateEventProviderAccepted, want: CandidateStatusFresh},
		{name: "stale validation recovers", from: CandidateStatusStale, event: CandidateEventValidationSucceeded, want: CandidateStatusFresh},
		{name: "acquire failure", from: CandidateStatusFresh, event: CandidateEventValidationFailed, want: CandidateStatusBroken},
		{name: "broken remains recoverable only by explicit success", from: CandidateStatusBroken, event: CandidateEventValidationSucceeded, want: CandidateStatusFresh},
		{name: "manual retirement", from: CandidateStatusStale, event: CandidateEventManualRetirement, want: CandidateStatusRetired},
		{name: "retired is terminal", from: CandidateStatusRetired, event: CandidateEventProviderAccepted, want: CandidateStatusRetired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TransitionCandidateStatus(tc.from, tc.event)
			if err != nil {
				t.Fatalf("transition error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("transition = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTransitionCandidateStatusRejectsUnknownEvent(t *testing.T) {
	if _, err := TransitionCandidateStatus(CandidateStatusFresh, CandidateStateEvent("unknown")); err != ErrInvalidCandidateTransition {
		t.Fatalf("unknown event error = %v, want %v", err, ErrInvalidCandidateTransition)
	}
}

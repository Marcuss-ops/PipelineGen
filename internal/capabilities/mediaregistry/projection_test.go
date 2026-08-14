package mediaregistry

import (
	"errors"
	"testing"
)

func TestValidateProjectionTransition_FailureOriented(t *testing.T) {
	tests := []struct {
		from ProjectionStatus
		to   ProjectionStatus
		ok   bool
	}{
		{ProjectionBuilding, ProjectionValidating, true},
		{ProjectionValidating, ProjectionReady, true},
		{ProjectionReady, ProjectionActive, true},
		{ProjectionActive, ProjectionRetired, true},
		{ProjectionFailed, ProjectionFailedCleaned, true},
		{ProjectionBuilding, ProjectionActive, false},
		{ProjectionFailed, ProjectionActive, false},
		{ProjectionFailedCleaned, ProjectionActive, false},
		{ProjectionFailedCleaned, ProjectionRetired, false},
		{ProjectionRetired, ProjectionBuilding, false},
	}
	for _, tt := range tests {
		err := ValidateProjectionTransition(tt.from, tt.to)
		if tt.ok && err != nil {
			t.Errorf("%s -> %s: unexpected error: %v", tt.from, tt.to, err)
		}
		if !tt.ok && !errors.Is(err, ErrInvalidProjectionTransition) {
			t.Errorf("%s -> %s: error=%v, want ErrInvalidProjectionTransition", tt.from, tt.to, err)
		}
	}
}

func TestValidateProjectionSequence_RejectsLagAndAhead(t *testing.T) {
	if err := ValidateProjectionSequence(17, 17); err != nil {
		t.Fatalf("equal sequence rejected: %v", err)
	}
	if err := ValidateProjectionSequence(16, 17); !errors.Is(err, ErrProjectionSequenceLag) {
		t.Fatalf("lag error=%v, want ErrProjectionSequenceLag", err)
	}
	if err := ValidateProjectionSequence(18, 17); !errors.Is(err, ErrProjectionSequenceAhead) {
		t.Fatalf("ahead error=%v, want ErrProjectionSequenceAhead", err)
	}
}

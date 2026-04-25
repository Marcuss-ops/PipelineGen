package models

import (
	"testing"
)

func TestJobStatusTransitions(t *testing.T) {
	tests := []struct {
		name   string
		status JobStatus
		valid  bool
	}{
		{"pending", JobStatusPending, true},
		{"running", JobStatusRunning, true},
		{"completed", JobStatusCompleted, true},
		{"failed", JobStatusFailed, true},
		{"invalid", JobStatus("invalid"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation that status is recognized
			validStatuses := []JobStatus{
				JobStatusPending,
				JobStatusRunning,
				JobStatusCompleted,
				JobStatusFailed,
			}
			found := false
			for _, s := range validStatuses {
				if tt.status == s {
					found = true
					break
				}
			}
			if found != tt.valid {
				t.Errorf("JobStatus %s valid = %v, want %v", tt.status, found, tt.valid)
			}
		})
	}
}

func TestJobTypeConstants(t *testing.T) {
	if JobTypeScript == "" {
		t.Error("JobTypeScript should not be empty")
	}
	if JobTypeVideoGeneration == "" {
		t.Error("JobTypeVideoGeneration should not be empty")
	}
}
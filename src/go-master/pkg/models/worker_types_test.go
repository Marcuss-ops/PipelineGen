package models

import (
	"testing"
)

func TestWorkerStatusTransitions(t *testing.T) {
	tests := []struct {
		name   string
		status WorkerStatus
		valid  bool
	}{
		{"idle", WorkerIdle, true},
		{"busy", WorkerBusy, true},
		{"offline", WorkerOffline, true},
		{"invalid", WorkerStatus("invalid"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validStatuses := []WorkerStatus{
				WorkerIdle,
				WorkerBusy,
				WorkerOffline,
			}
			found := false
			for _, s := range validStatuses {
				if tt.status == s {
					found = true
					break
				}
			}
			if found != tt.valid {
				t.Errorf("WorkerStatus %s valid = %v, want %v", tt.status, found, tt.valid)
			}
		})
	}
}

func TestWorkerTypeConstants(t *testing.T) {
	if WorkerCapabilityScript == "" {
		t.Error("WorkerCapabilityScript should not be empty")
	}
	if WorkerCapabilityVideoGen == "" {
		t.Error("WorkerCapabilityVideoGen should not be empty")
	}
}
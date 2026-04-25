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
		{"idle", WorkerStatusIdle, true},
		{"busy", WorkerStatusBusy, true},
		{"offline", WorkerStatusOffline, true},
		{"invalid", WorkerStatus("invalid"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validStatuses := []WorkerStatus{
				WorkerStatusIdle,
				WorkerStatusBusy,
				WorkerStatusOffline,
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
	if WorkerTypeScript == "" {
		t.Error("WorkerTypeScript should not be empty")
	}
	if WorkerTypeRender == "" {
		t.Error("WorkerTypeRender should not be empty")
	}
}
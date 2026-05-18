package artlist

import (
	"testing"
)

func TestGetIntFromResult(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected int
	}{
		{"nil map", nil, "key", 0},
		{"empty map", map[string]interface{}{}, "key", 0},
		{"int value", map[string]interface{}{"key": 42}, "key", 42},
		{"float64 value", map[string]interface{}{"key": float64(42)}, "key", 42},
		{"string value", map[string]interface{}{"key": "not_a_number"}, "key", 0},
		{"missing key", map[string]interface{}{"other": 1}, "key", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIntFromResult(tt.m, tt.key)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestRunTagResponseStructure(t *testing.T) {
	resp := &RunTagResponse{
		OK:     true,
		Status: "completed",
		RunID:  "run-123",
	}
	if !resp.OK {
		t.Error("expected OK to be true")
	}
	if resp.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", resp.Status)
	}
}

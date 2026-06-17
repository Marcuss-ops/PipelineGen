package ptrutil

import (
	"testing"
)

func TestBoolDefault(t *testing.T) {
	tests := []struct {
		name     string
		ptr      *bool
		def      bool
		expected bool
	}{
		{"nil_with_false_default", nil, false, false},
		{"nil_with_true_default", nil, true, true},
		{"ptr_true", boolPtr(true), false, true},
		{"ptr_false", boolPtr(false), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BoolDefault(tt.ptr, tt.def)
			if got != tt.expected {
				t.Errorf("BoolDefault(%v, %v) = %v, want %v", tt.ptr, tt.def, got, tt.expected)
			}
		})
	}
}

func TestBool(t *testing.T) {
	ptr := Bool(true)
	if ptr == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *ptr != true {
		t.Errorf("expected true, got %v", *ptr)
	}

	ptr = Bool(false)
	if ptr == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *ptr != false {
		t.Errorf("expected false, got %v", *ptr)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

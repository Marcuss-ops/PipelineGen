package finaljob

import (
	"encoding/json"
	"testing"
)

func TestCoercionsHandleWireAndLegacyShapes(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int64
	}{
		{"nil", nil, 0},
		{"int", 42, 42},
		{"int64", int64(43), 43},
		{"float", 44.9, 44},
		{"json number", json.Number("45"), 45},
		{"numeric string", " 46 ", 46},
		{"invalid string", "nope", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Int64(test.value); got != test.want {
				t.Fatalf("Int64(%#v) = %d, want %d", test.value, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, ""},
		{"string", " value ", "value"},
		{"number", 47, "47"},
		{"nil sentinel", "<nil>", ""},
	} {
		t.Run("string/"+test.name, func(t *testing.T) {
			if got := String(test.value); got != test.want {
				t.Fatalf("String(%#v) = %q, want %q", test.value, got, test.want)
			}
		})
	}

	for _, test := range []struct {
		name  string
		value any
		want  bool
	}{
		{"bool", true, true},
		{"string true", " true ", true},
		{"string one", "1", true},
		{"string false", "false", false},
		{"nil", nil, false},
	} {
		t.Run("bool/"+test.name, func(t *testing.T) {
			if got := Bool(test.value); got != test.want {
				t.Fatalf("Bool(%#v) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

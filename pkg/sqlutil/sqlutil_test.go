package sqlutil

import (
	"errors"
	"testing"
)

func TestBoolInt(t *testing.T) {
	if BoolInt(true) != 1 {
		t.Errorf("BoolInt(true) = %d; want 1", BoolInt(true))
	}
	if BoolInt(false) != 0 {
		t.Errorf("BoolInt(false) = %d; want 0", BoolInt(false))
	}
}

func TestNullString(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"hello", "hello"},
		{"  trimmed  ", "  trimmed  "},
		{"", nil},
		{"   ", nil},
		{"\t\n", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			actual := NullString(tt.input)
			if actual != tt.expected {
				t.Errorf("NullString(%q) = %v; want %v", tt.input, actual, tt.expected)
			}
		})
	}
}

func TestIsUniqueConstraintErr(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "unique constraint failed lowercase",
			err:      errors.New("unique constraint failed: users.email"),
			expected: true,
		},
		{
			name:     "unique constraint failed uppercase",
			err:      errors.New("UNIQUE CONSTRAINT FAILED: USERS.EMAIL"),
			expected: true,
		},
		{
			name:     "mixed case",
			err:      errors.New("Some error occurred: Unique Constraint Failed"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("sql: no rows in result set"),
			expected: false,
		},
		{
			name:     "partial match",
			err:      errors.New("unique constraint"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := IsUniqueConstraintErr(tt.err)
			if actual != tt.expected {
				t.Errorf("IsUniqueConstraintErr() = %v; want %v", actual, tt.expected)
			}
		})
	}
}

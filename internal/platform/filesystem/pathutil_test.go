package filesystem

import (
	"testing"
)

func TestSafeFolderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "Hello World"},
		{"hello", "hello"},
		{"", "untitled"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SafeFolderName(tt.input)
			if got != tt.expected {
				t.Errorf("SafeFolderName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

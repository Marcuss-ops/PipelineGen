package ollama

import (
	"testing"
)

func TestNewGenerator(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		gen := NewGenerator(nil)
		if gen == nil {
			t.Error("Expected non-nil generator")
		}
	})
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		endpoint string
		expected string
	}{
		{
			name:     "simple path",
			base:     "http://localhost:11434",
			endpoint: "/api/generate",
			expected: "http://localhost:11434/api/generate",
		},
		{
			name:     "trailing slash",
			base:     "http://localhost:11434/",
			endpoint: "/api/generate",
			expected: "http://localhost:11434/api/generate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildURL(tt.base, tt.endpoint)
			if result != tt.expected {
				t.Errorf("buildURL() = %s, want %s", result, tt.expected)
			}
		})
	}
}